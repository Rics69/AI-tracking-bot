package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

const userColumns = `telegram_id, first_name, COALESCE(username, ''), is_head, weekly_report, removed_at, created_at, updated_at`

// UserRepository implements user.Repository.
type UserRepository struct {
	pool *pgxpool.Pool
}

var _ user.Repository = (*UserRepository)(nil)

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

func (r *UserRepository) Upsert(ctx context.Context, u user.User) (user.User, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO users (telegram_id, first_name, username, is_head)
		VALUES ($1, $2, NULLIF($3, ''), $4)
		ON CONFLICT (telegram_id) DO UPDATE
		SET first_name = EXCLUDED.first_name, username = EXCLUDED.username, updated_at = now()
		RETURNING `+userColumns,
		u.TelegramID, u.FirstName, u.Username, u.IsHead)
	saved, err := scanUser(row)
	if err != nil {
		return user.User{}, fmt.Errorf("upsert user: %w", err)
	}
	return saved, nil
}

func (r *UserRepository) Add(ctx context.Context, u user.User) (user.User, error) {
	return addUser(ctx, r.pool, u)
}

// addUser runs on a pool or inside a transaction (see InviteRepository.Redeem).
func addUser(ctx context.Context, db interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, u user.User) (user.User, error) {
	row := db.QueryRow(ctx, `
		INSERT INTO users (telegram_id, first_name, username, is_head)
		VALUES ($1, $2, NULLIF($3, ''), $4)
		ON CONFLICT (telegram_id) DO UPDATE
		SET first_name = EXCLUDED.first_name, username = EXCLUDED.username,
		    is_head = users.is_head OR EXCLUDED.is_head, removed_at = NULL, updated_at = now()
		RETURNING `+userColumns,
		u.TelegramID, u.FirstName, u.Username, u.IsHead)
	saved, err := scanUser(row)
	if err != nil {
		return user.User{}, fmt.Errorf("add user: %w", err)
	}
	return saved, nil
}

func (r *UserRepository) Get(ctx context.Context, telegramID int64) (user.User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE telegram_id = $1`, telegramID))
	if errors.Is(err, pgx.ErrNoRows) {
		return user.User{}, user.ErrNotFound
	}
	if err != nil {
		return user.User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

func (r *UserRepository) Remove(ctx context.Context, telegramID int64) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE users SET removed_at = COALESCE(removed_at, now()), updated_at = now()
		WHERE telegram_id = $1`, telegramID)
	if err != nil {
		return fmt.Errorf("remove user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return user.ErrNotFound
	}
	return nil
}

func (r *UserRepository) SetWeeklyReport(ctx context.Context, telegramID int64, on bool) (user.User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, `
		UPDATE users SET weekly_report = $2, updated_at = now() WHERE telegram_id = $1
		RETURNING `+userColumns, telegramID, on))
	if errors.Is(err, pgx.ErrNoRows) {
		return user.User{}, user.ErrNotFound
	}
	if err != nil {
		return user.User{}, fmt.Errorf("set weekly report: %w", err)
	}
	return u, nil
}

func (r *UserRepository) CountActive(ctx context.Context) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE removed_at IS NULL`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

func (r *UserRepository) List(ctx context.Context) ([]user.User, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+userColumns+` FROM users
		ORDER BY removed_at IS NOT NULL, is_head DESC, first_name, telegram_id`)
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

func scanUser(row pgx.Row) (user.User, error) {
	var u user.User
	err := row.Scan(&u.TelegramID, &u.FirstName, &u.Username, &u.IsHead, &u.WeeklyReport, &u.RemovedAt, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}
