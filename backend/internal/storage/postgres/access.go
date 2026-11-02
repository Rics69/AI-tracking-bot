package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/access"
)

// AccessRepository implements access.Store.
type AccessRepository struct {
	pool *pgxpool.Pool
}

var _ access.Store = (*AccessRepository)(nil)

func NewAccessRepository(pool *pgxpool.Pool) *AccessRepository {
	return &AccessRepository{pool: pool}
}

func (r *AccessRepository) Add(ctx context.Context, e access.Entitlement) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO entitlements (user_id, product, source, starts_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, e.UserID, e.Product, e.Source, e.StartsAt, e.ExpiresAt)
	if err != nil {
		return fmt.Errorf("add entitlement: %w", err)
	}
	return nil
}

func (r *AccessRepository) Until(ctx context.Context, userID int64, product string) (*time.Time, error) {
	var until *time.Time
	err := r.pool.QueryRow(ctx, `SELECT max(expires_at) FROM entitlements WHERE user_id = $1 AND product = $2`,
		userID, product).Scan(&until)
	if err != nil {
		return nil, fmt.Errorf("get entitlement: %w", err)
	}
	return until, nil
}

func (r *AccessRepository) Close(ctx context.Context, userID int64, product string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE entitlements SET expires_at = $3
		WHERE user_id = $1 AND product = $2 AND expires_at > $3`, userID, product, at)
	if err != nil {
		return fmt.Errorf("close entitlement: %w", err)
	}
	return nil
}

func (r *AccessRepository) Endings(ctx context.Context, product string, from time.Time) ([]access.Ending, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT user_id, max(expires_at) FROM entitlements WHERE product = $1
		GROUP BY user_id HAVING max(expires_at) >= $2`, product, from)
	if err != nil {
		return nil, fmt.Errorf("list endings: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[access.Ending])
	if err != nil {
		return nil, fmt.Errorf("list endings: %w", err)
	}
	return out, nil
}

func (r *AccessRepository) Active(ctx context.Context, product string, at time.Time) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(DISTINCT user_id) FROM entitlements WHERE product = $1 AND expires_at > $2`,
		product, at).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count members: %w", err)
	}
	return n, nil
}

func (r *AccessRepository) AddPromo(ctx context.Context, p access.Promo) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO promo_codes (code_hash, product, days, max_uses, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, p.Hash, p.Product, p.Days, p.MaxUses, p.ExpiresAt)
	if err != nil {
		return fmt.Errorf("add promo: %w", err)
	}
	return nil
}

func (r *AccessRepository) RedeemPromo(ctx context.Context, hash []byte, userID int64, at time.Time) (access.Promo, error) {
	var p access.Promo
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var used int
		err := tx.QueryRow(ctx, `SELECT product, days, max_uses, used, expires_at FROM promo_codes
			WHERE code_hash = $1 FOR UPDATE`, hash).Scan(&p.Product, &p.Days, &p.MaxUses, &used, &p.ExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return access.ErrPromoInvalid
		}
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO promo_uses (code_hash, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, hash, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return access.ErrPromoUsed
		}
		if used >= p.MaxUses || p.ExpiresAt != nil && !p.ExpiresAt.After(at) {
			return access.ErrPromoInvalid
		}
		_, err = tx.Exec(ctx, `UPDATE promo_codes SET used = used + 1 WHERE code_hash = $1`, hash)
		return err
	})
	if err != nil {
		if errors.Is(err, access.ErrPromoInvalid) || errors.Is(err, access.ErrPromoUsed) {
			return access.Promo{}, err
		}
		return access.Promo{}, fmt.Errorf("redeem promo: %w", err)
	}
	return p, nil
}
