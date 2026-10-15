package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

const userColumns = `telegram_id, first_name, COALESCE(username, ''), created_at, updated_at`

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
		INSERT INTO users (telegram_id, first_name, username)
		VALUES ($1, $2, NULLIF($3, ''))
		ON CONFLICT (telegram_id) DO UPDATE
		SET first_name = EXCLUDED.first_name, username = EXCLUDED.username, updated_at = now()
		RETURNING `+userColumns,
		u.TelegramID, u.FirstName, u.Username)
	saved, err := scanUser(row)
	if err != nil {
		return user.User{}, fmt.Errorf("upsert user: %w", err)
	}
	return saved, nil
}

func (r *UserRepository) List(ctx context.Context) ([]user.User, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY first_name, telegram_id`)
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
	err := row.Scan(&u.TelegramID, &u.FirstName, &u.Username, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}
