-- +goose Up
-- Incomes join expenses in one table of operations: one history, one balance.
-- The kind follows from the category (salary is an income, food an expense);
-- it is stored for filtering and checked against the category.
ALTER TABLE expenses RENAME TO operations;
ALTER TABLE operations RENAME COLUMN spent_at TO occurred_on;
ALTER TABLE operations ADD COLUMN kind TEXT NOT NULL DEFAULT 'expense';
ALTER TABLE operations ALTER COLUMN kind DROP DEFAULT;

ALTER TABLE operations DROP CONSTRAINT expenses_category_check;
ALTER TABLE operations ADD CONSTRAINT operations_category_check CHECK (
    (kind = 'expense' AND category IN
        ('food', 'transport', 'housing', 'entertainment', 'health', 'shopping', 'other'))
    OR (kind = 'income' AND category IN
        ('salary', 'freelance', 'gift', 'interest', 'refund', 'other_income')));

ALTER TABLE operations RENAME CONSTRAINT expenses_pkey TO operations_pkey;
ALTER TABLE operations RENAME CONSTRAINT expenses_amount_check TO operations_amount_check;
ALTER TABLE operations RENAME CONSTRAINT expenses_user_id_fkey TO operations_user_id_fkey;
ALTER INDEX expenses_user_spent_at_idx RENAME TO operations_user_occurred_on_idx;
ALTER INDEX expenses_spent_at_idx RENAME TO operations_occurred_on_idx;
ALTER SEQUENCE expenses_id_seq RENAME TO operations_id_seq;

-- +goose Down
DELETE FROM operations WHERE kind = 'income';
ALTER SEQUENCE operations_id_seq RENAME TO expenses_id_seq;
ALTER INDEX operations_occurred_on_idx RENAME TO expenses_spent_at_idx;
ALTER INDEX operations_user_occurred_on_idx RENAME TO expenses_user_spent_at_idx;
ALTER TABLE operations RENAME CONSTRAINT operations_user_id_fkey TO expenses_user_id_fkey;
ALTER TABLE operations RENAME CONSTRAINT operations_amount_check TO expenses_amount_check;
ALTER TABLE operations RENAME CONSTRAINT operations_pkey TO expenses_pkey;
ALTER TABLE operations DROP CONSTRAINT operations_category_check;
ALTER TABLE operations ADD CONSTRAINT expenses_category_check CHECK (category IN
    ('food', 'transport', 'housing', 'entertainment', 'health', 'shopping', 'other'));
ALTER TABLE operations DROP COLUMN kind;
ALTER TABLE operations RENAME COLUMN occurred_on TO spent_at;
ALTER TABLE operations RENAME TO expenses;
