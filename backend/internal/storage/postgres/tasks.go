package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/tasks"
)

// TaskRepository implements tasks.Store.
type TaskRepository struct {
	pool *pgxpool.Pool
}

var _ tasks.Store = (*TaskRepository)(nil)

func NewTaskRepository(pool *pgxpool.Pool) *TaskRepository {
	return &TaskRepository{pool: pool}
}

const taskColumns = `id, user_id, title, due_on, COALESCE(due_time, ''), repeat, goal_id, done_at, created_at`

func (r *TaskRepository) list(ctx context.Context, where string, args ...any) ([]tasks.Task, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+taskColumns+` FROM tasks WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[tasks.Task])
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return out, nil
}

func (r *TaskRepository) List(ctx context.Context, userID int64) ([]tasks.Task, error) {
	return r.list(ctx, `user_id = $1 ORDER BY due_on, id`, userID)
}

func (r *TaskRepository) Open(ctx context.Context) ([]tasks.Task, error) {
	return r.list(ctx, `done_at IS NULL`)
}

func (r *TaskRepository) one(row pgx.Row, what string) (tasks.Task, error) {
	var t tasks.Task
	err := row.Scan(&t.ID, &t.UserID, &t.Title, &t.DueOn, &t.DueTime, &t.Repeat, &t.GoalID, &t.DoneAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return tasks.Task{}, tasks.ErrNotFound
	}
	if err != nil {
		return tasks.Task{}, fmt.Errorf("%s: %w", what, err)
	}
	return t, nil
}

func (r *TaskRepository) Get(ctx context.Context, userID, id int64) (tasks.Task, error) {
	return r.one(r.pool.QueryRow(ctx, `SELECT `+taskColumns+` FROM tasks WHERE user_id = $1 AND id = $2`, userID, id), "get task")
}

func (r *TaskRepository) Create(ctx context.Context, t tasks.Task) (tasks.Task, error) {
	return r.one(r.pool.QueryRow(ctx, `
		INSERT INTO tasks (user_id, title, due_on, due_time, repeat, goal_id)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6)
		RETURNING `+taskColumns, t.UserID, t.Title, t.DueOn, t.DueTime, t.Repeat, t.GoalID), "create task")
}

func (r *TaskRepository) Update(ctx context.Context, t tasks.Task) (tasks.Task, error) {
	return r.one(r.pool.QueryRow(ctx, `
		UPDATE tasks SET title = $3, due_on = $4, due_time = NULLIF($5, ''), repeat = $6, done_at = $7
		WHERE user_id = $1 AND id = $2
		RETURNING `+taskColumns, t.UserID, t.ID, t.Title, t.DueOn, t.DueTime, t.Repeat, t.DoneAt), "update task")
}

func (r *TaskRepository) Delete(ctx context.Context, userID, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM tasks WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tasks.ErrNotFound
	}
	return nil
}
