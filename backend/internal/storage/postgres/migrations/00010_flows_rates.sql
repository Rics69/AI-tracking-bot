-- +goose Up
-- The morning message no longer needs a plan: it also goes to those with only
-- a goal or a regular payment, so its settings belong to the user.
ALTER TABLE users
    ADD COLUMN reminder_hour SMALLINT NOT NULL DEFAULT 9 CHECK (reminder_hour BETWEEN 0 AND 23),
    ADD COLUMN reminders     BOOLEAN  NOT NULL DEFAULT true,
    ADD COLUMN show_rates    BOOLEAN  NOT NULL DEFAULT true,
    ADD COLUMN goal_updates  BOOLEAN  NOT NULL DEFAULT true;
UPDATE users u SET reminder_hour = p.reminder_hour, reminders = p.reminders
FROM budget_plans p WHERE p.user_id = u.telegram_id;
ALTER TABLE budget_plans
    DROP COLUMN reminder_hour,
    DROP COLUMN reminders;

-- The dialog a user is in the middle of: onboarding, the dream calculator.
CREATE TABLE user_flows (
    user_id    BIGINT      PRIMARY KEY REFERENCES users (telegram_id) ON DELETE CASCADE,
    flow       TEXT        NOT NULL,
    step       TEXT        NOT NULL,
    data       JSONB       NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Official rates of the Bank of Russia: rubles for one unit.
CREATE TABLE rates (
    code  TEXT          NOT NULL,
    date  DATE          NOT NULL,
    value NUMERIC(12,4) NOT NULL,
    PRIMARY KEY (code, date)
);

-- +goose Down
DROP TABLE rates;
DROP TABLE user_flows;
ALTER TABLE budget_plans
    ADD COLUMN reminder_hour SMALLINT NOT NULL DEFAULT 9 CHECK (reminder_hour BETWEEN 0 AND 23),
    ADD COLUMN reminders     BOOLEAN  NOT NULL DEFAULT true;
UPDATE budget_plans p SET reminder_hour = u.reminder_hour, reminders = u.reminders
FROM users u WHERE p.user_id = u.telegram_id;
ALTER TABLE users
    DROP COLUMN goal_updates,
    DROP COLUMN show_rates,
    DROP COLUMN reminders,
    DROP COLUMN reminder_hour;
