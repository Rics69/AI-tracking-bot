package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

const inviteColumns = `id, created_at, expires_at, used_at, used_by, revoked_at`

// InviteRepository implements family.Invites.
type InviteRepository struct {
	pool *pgxpool.Pool
}

var _ family.Invites = (*InviteRepository)(nil)

func NewInviteRepository(pool *pgxpool.Pool) *InviteRepository {
	return &InviteRepository{pool: pool}
}

func (r *InviteRepository) Create(ctx context.Context, codeHash []byte, expiresAt time.Time) (family.Invite, error) {
	inv, err := scanInvite(r.pool.QueryRow(ctx, `
		INSERT INTO invites (code_hash, expires_at) VALUES ($1, $2)
		RETURNING `+inviteColumns, codeHash, expiresAt))
	if err != nil {
		return family.Invite{}, fmt.Errorf("create invite: %w", err)
	}
	return inv, nil
}

func (r *InviteRepository) List(ctx context.Context, since time.Time) ([]family.Invite, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+inviteColumns+` FROM invites
		WHERE created_at >= $1 ORDER BY created_at DESC, id DESC`, since)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	invites, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (family.Invite, error) {
		return scanInvite(row)
	})
	if err != nil {
		return nil, fmt.Errorf("scan invites: %w", err)
	}
	return invites, nil
}

func (r *InviteRepository) Revoke(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE invites SET revoked_at = now()
		WHERE id = $1 AND revoked_at IS NULL AND used_at IS NULL AND expires_at > now()`, id)
	if err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return family.ErrInviteNotFound
	}
	return nil
}

// Redeem uses the invite and adds the member in one transaction: the row lock
// makes two people opening the same link at once impossible to both succeed.
func (r *InviteRepository) Redeem(ctx context.Context, codeHash []byte, u user.User) (user.User, error) {
	var member user.User
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var id int64
		err := tx.QueryRow(ctx, `
			SELECT id FROM invites
			WHERE code_hash = $1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > now()
			FOR UPDATE`, codeHash).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return family.ErrInviteInvalid
		}
		if err != nil {
			return fmt.Errorf("find invite: %w", err)
		}
		if member, err = addUser(ctx, tx, u); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE invites SET used_at = now(), used_by = $2 WHERE id = $1`, id, u.TelegramID); err != nil {
			return fmt.Errorf("use invite: %w", err)
		}
		return nil
	})
	if err != nil {
		return user.User{}, err
	}
	return member, nil
}

func scanInvite(row pgx.Row) (family.Invite, error) {
	var i family.Invite
	err := row.Scan(&i.ID, &i.CreatedAt, &i.ExpiresAt, &i.UsedAt, &i.UsedBy, &i.RevokedAt)
	return i, err
}

// SettingsRepository implements family.Settings.
type SettingsRepository struct {
	pool *pgxpool.Pool
}

var _ family.Settings = (*SettingsRepository)(nil)

func NewSettingsRepository(pool *pgxpool.Pool) *SettingsRepository {
	return &SettingsRepository{pool: pool}
}

func (r *SettingsRepository) Get(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := r.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get setting %s: %w", key, err)
	}
	return v, true, nil
}

func (r *SettingsRepository) Set(ctx context.Context, key, value string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, value)
	if err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}
