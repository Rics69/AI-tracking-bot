package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

const userColumns = `telegram_id, family_id, first_name, COALESCE(username, ''), is_head, weekly_report,
	COALESCE(timezone, ''), consented_at, blocked_at, last_seen_at, created_at, updated_at`

// UserRepository implements user.Repository.
type UserRepository struct {
	pool *pgxpool.Pool
}

var _ user.Repository = (*UserRepository)(nil)

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

func (r *UserRepository) Admit(ctx context.Context, u user.User) (user.User, error) {
	saved, err := scanUser(r.pool.QueryRow(ctx, `
		UPDATE users SET first_name = $2, username = NULLIF($3, ''), last_seen_at = now(),
		    updated_at = CASE WHEN first_name = $2 AND username IS NOT DISTINCT FROM NULLIF($3, '')
		                      THEN updated_at ELSE now() END
		WHERE telegram_id = $1
		RETURNING `+userColumns, u.TelegramID, u.FirstName, u.Username))
	if err == nil {
		return saved, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return user.User{}, fmt.Errorf("admit user: %w", err)
	}

	// Someone new: a family of their own. If the same user is being created
	// concurrently, the insert finds the row taken and the transaction (with
	// its family) rolls back; the update above then finds the user.
	err = pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var familyID int64
		if err := tx.QueryRow(ctx, `INSERT INTO families DEFAULT VALUES RETURNING id`).Scan(&familyID); err != nil {
			return fmt.Errorf("create family: %w", err)
		}
		saved, err = scanUser(tx.QueryRow(ctx, `
			INSERT INTO users (telegram_id, family_id, first_name, username, is_head, last_seen_at)
			VALUES ($1, $2, $3, NULLIF($4, ''), true, now())
			ON CONFLICT (telegram_id) DO NOTHING
			RETURNING `+userColumns, u.TelegramID, familyID, u.FirstName, u.Username))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return r.Admit(ctx, u)
	}
	if err != nil {
		return user.User{}, fmt.Errorf("create user: %w", err)
	}
	return saved, nil
}

func (r *UserRepository) Get(ctx context.Context, telegramID int64) (user.User, error) {
	return getUser(ctx, r.pool, telegramID, "")
}

// getUser reads a user; lock is "" or a locking clause such as "FOR UPDATE".
func getUser(ctx context.Context, db interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, telegramID int64, lock string) (user.User, error) {
	u, err := scanUser(db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE telegram_id = $1 `+lock, telegramID))
	if errors.Is(err, pgx.ErrNoRows) {
		return user.User{}, user.ErrNotFound
	}
	if err != nil {
		return user.User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

func (r *UserRepository) Family(ctx context.Context, familyID int64) ([]user.User, error) {
	return r.list(ctx, `WHERE family_id = $1`, familyID)
}

func (r *UserRepository) All(ctx context.Context) ([]user.User, error) {
	return r.list(ctx, ``)
}

func (r *UserRepository) list(ctx context.Context, where string, args ...any) ([]user.User, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+userColumns+` FROM users `+where+`
		ORDER BY family_id, is_head DESC, first_name, telegram_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	users, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (user.User, error) {
		return scanUser(row)
	})
	if err != nil {
		return nil, fmt.Errorf("scan users: %w", err)
	}
	return users, nil
}

func (r *UserRepository) SetWeeklyReport(ctx context.Context, telegramID int64, on bool) (user.User, error) {
	return r.set(ctx, telegramID, `weekly_report = $2`, on)
}

func (r *UserRepository) SetTimezone(ctx context.Context, telegramID int64, tz string) (user.User, error) {
	return r.set(ctx, telegramID, `timezone = NULLIF($2, '')`, tz)
}

func (r *UserRepository) SetConsent(ctx context.Context, telegramID int64, at time.Time) (user.User, error) {
	return r.set(ctx, telegramID, `consented_at = $2`, at)
}

func (r *UserRepository) set(ctx context.Context, telegramID int64, assignment string, value any) (user.User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, `
		UPDATE users SET `+assignment+`, updated_at = now() WHERE telegram_id = $1
		RETURNING `+userColumns, telegramID, value))
	if errors.Is(err, pgx.ErrNoRows) {
		return user.User{}, user.ErrNotFound
	}
	if err != nil {
		return user.User{}, fmt.Errorf("update user: %w", err)
	}
	return u, nil
}

func (r *UserRepository) Detach(ctx context.Context, telegramID int64) (user.User, error) {
	var detached user.User
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		u, err := getUser(ctx, tx, telegramID, "FOR UPDATE")
		if err != nil {
			return err
		}
		detached, err = moveUser(ctx, tx, u, 0, true)
		return err
	})
	if err != nil {
		return user.User{}, err
	}
	return detached, nil
}

// moveUser puts u into family toFamily (0: a new family) with the given role
// and deletes the family u leaves if nobody is left in it.
func moveUser(ctx context.Context, tx pgx.Tx, u user.User, toFamily int64, head bool) (user.User, error) {
	if toFamily == 0 {
		if err := tx.QueryRow(ctx, `INSERT INTO families DEFAULT VALUES RETURNING id`).Scan(&toFamily); err != nil {
			return user.User{}, fmt.Errorf("create family: %w", err)
		}
	}
	moved, err := scanUser(tx.QueryRow(ctx, `
		UPDATE users SET family_id = $2, is_head = $3, updated_at = now() WHERE telegram_id = $1
		RETURNING `+userColumns, u.TelegramID, toFamily, head))
	if err != nil {
		return user.User{}, fmt.Errorf("move user: %w", err)
	}
	if err := deleteEmptyFamily(ctx, tx, u.FamilyID); err != nil {
		return user.User{}, err
	}
	return moved, nil
}

func deleteEmptyFamily(ctx context.Context, tx pgx.Tx, familyID int64) error {
	_, err := tx.Exec(ctx, `
		DELETE FROM families f WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM users WHERE family_id = f.id)`, familyID)
	if err != nil {
		return fmt.Errorf("delete empty family: %w", err)
	}
	return nil
}

// Delete removes the user; expenses, dialog history, report runs and LLM
// usage go with them (ON DELETE CASCADE).
func (r *UserRepository) Delete(ctx context.Context, telegramID int64) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var familyID int64
		err := tx.QueryRow(ctx, `DELETE FROM users WHERE telegram_id = $1 RETURNING family_id`, telegramID).Scan(&familyID)
		if errors.Is(err, pgx.ErrNoRows) {
			return user.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		return deleteEmptyFamily(ctx, tx, familyID)
	})
}

func scanUser(row pgx.Row) (user.User, error) {
	var u user.User
	err := row.Scan(&u.TelegramID, &u.FamilyID, &u.FirstName, &u.Username, &u.IsHead, &u.WeeklyReport,
		&u.Timezone, &u.ConsentedAt, &u.BlockedAt, &u.LastSeenAt, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}
