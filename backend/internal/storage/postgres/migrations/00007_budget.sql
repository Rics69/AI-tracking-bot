-- +goose Up
-- Goals, the pay plan and regular payments. All of it is optional: without a
-- plan the bot is plain expense and income tracking. The daily limit is
-- computed from these tables by code (package budget) and is not stored.
CREATE TABLE goals (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT        NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    title       TEXT          NOT NULL,
    target      NUMERIC(12,2) NOT NULL CHECK (target > 0),
    deadline    DATE,
    achieved_on DATE,          -- the user's date when the savings first reached the target
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);
CREATE INDEX goals_user_idx ON goals (user_id);

-- Money put aside for a goal; a negative amount takes money back out.
-- Deleting a goal deletes its deposits: the money is free again.
CREATE TABLE goal_deposits (
    id          BIGSERIAL PRIMARY KEY,
    goal_id     BIGINT        NOT NULL REFERENCES goals (id) ON DELETE CASCADE,
    user_id     BIGINT        NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    amount      NUMERIC(12,2) NOT NULL CHECK (amount <> 0),
    occurred_on DATE          NOT NULL,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);
CREATE INDEX goal_deposits_goal_idx ON goal_deposits (goal_id);
CREATE INDEX goal_deposits_user_idx ON goal_deposits (user_id, occurred_on);

-- How money comes: paydays (one or two days of the month) and, optionally,
-- the expected income of a period and how much of it goes to goals.
CREATE TABLE budget_plans (
    user_id           BIGINT        PRIMARY KEY REFERENCES users (telegram_id) ON DELETE CASCADE,
    paydays           SMALLINT[]    NOT NULL CHECK (
        cardinality(paydays) BETWEEN 1 AND 2 AND 1 <= ALL (paydays) AND 31 >= ALL (paydays)),
    expected_income   NUMERIC(12,2) CHECK (expected_income > 0),
    goal_contribution NUMERIC(12,2) CHECK (goal_contribution > 0),
    reminder_hour     SMALLINT      NOT NULL DEFAULT 9 CHECK (reminder_hour BETWEEN 0 AND 23),
    reminders         BOOLEAN       NOT NULL DEFAULT true,
    updated_at        TIMESTAMPTZ   NOT NULL DEFAULT now()
);

-- Rent, phone, subscriptions: a monthly amount on a day of the month.
CREATE TABLE regular_payments (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT        NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    title      TEXT          NOT NULL,
    amount     NUMERIC(12,2) NOT NULL CHECK (amount > 0),
    day        SMALLINT      NOT NULL CHECK (day BETWEEN 1 AND 31),
    category   TEXT          NOT NULL CHECK (category IN
        ('food', 'transport', 'housing', 'entertainment', 'health', 'shopping', 'other')),
    created_at TIMESTAMPTZ   NOT NULL DEFAULT now(),
    UNIQUE (id, user_id)
);
CREATE INDEX regular_payments_user_idx ON regular_payments (user_id);

-- An expense may pay one of its owner's regular payments, so the limit counts
-- the money once. The key includes user_id: nobody pays someone else's rent.
ALTER TABLE operations ADD COLUMN regular_payment_id BIGINT;
ALTER TABLE operations ADD CONSTRAINT operations_regular_payment_fkey
    FOREIGN KEY (regular_payment_id, user_id) REFERENCES regular_payments (id, user_id)
    ON DELETE SET NULL (regular_payment_id);
CREATE INDEX operations_regular_payment_idx ON operations (regular_payment_id)
    WHERE regular_payment_id IS NOT NULL;

-- One "send it once" table for every message the bot starts itself:
-- weekly reports and daily reminders.
ALTER TABLE report_runs RENAME TO notification_runs;
ALTER TABLE notification_runs
    ADD COLUMN kind       TEXT NOT NULL DEFAULT 'weekly',
    ADD COLUMN period_key TEXT;
UPDATE notification_runs SET period_key = to_char(week_start, 'YYYY-MM-DD');
ALTER TABLE notification_runs
    ALTER COLUMN period_key SET NOT NULL,
    ALTER COLUMN kind DROP DEFAULT,
    DROP CONSTRAINT report_runs_pkey,
    DROP COLUMN week_start,
    ADD PRIMARY KEY (kind, period_key, user_id);
ALTER TABLE notification_runs RENAME CONSTRAINT report_runs_user_id_fkey TO notification_runs_user_id_fkey;

-- +goose Down
ALTER TABLE notification_runs RENAME CONSTRAINT notification_runs_user_id_fkey TO report_runs_user_id_fkey;
DELETE FROM notification_runs WHERE kind <> 'weekly';
ALTER TABLE notification_runs ADD COLUMN week_start DATE;
UPDATE notification_runs SET week_start = period_key::date;
ALTER TABLE notification_runs
    DROP CONSTRAINT notification_runs_pkey,
    DROP COLUMN kind,
    DROP COLUMN period_key,
    ALTER COLUMN week_start SET NOT NULL,
    ADD CONSTRAINT report_runs_pkey PRIMARY KEY (week_start, user_id);
ALTER TABLE notification_runs RENAME TO report_runs;

DROP INDEX operations_regular_payment_idx;
ALTER TABLE operations DROP CONSTRAINT operations_regular_payment_fkey;
ALTER TABLE operations DROP COLUMN regular_payment_id;
DROP TABLE regular_payments;
DROP TABLE budget_plans;
DROP TABLE goal_deposits;
DROP TABLE goals;
