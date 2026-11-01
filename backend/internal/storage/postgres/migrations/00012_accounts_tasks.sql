-- +goose Up
-- Where the money is. Every user has one default account; operations without
-- an account belong to it, so all the history stays as it is.
CREATE TABLE accounts (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT        NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    name            TEXT          NOT NULL,
    kind            TEXT          NOT NULL DEFAULT 'card',
    currency        TEXT          NOT NULL DEFAULT 'RUB',
    opening_balance NUMERIC(14,2) NOT NULL DEFAULT 0,
    is_default      BOOLEAN       NOT NULL DEFAULT false,
    archived_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    UNIQUE (id, user_id)
);
CREATE UNIQUE INDEX accounts_default_idx ON accounts (user_id) WHERE is_default;
INSERT INTO accounts (user_id, name, is_default)
SELECT telegram_id, 'Основной', true FROM users;

-- amount stays in rubles: every sum in the service adds it up, so a foreign
-- operation needs no conversion when it is read. What was really paid is kept
-- beside it, in the currency of the operation.
ALTER TABLE operations
    ADD COLUMN currency        TEXT NOT NULL DEFAULT 'RUB',
    ADD COLUMN original_amount NUMERIC(12,2),
    ADD COLUMN account_id      BIGINT;
ALTER TABLE operations ADD CONSTRAINT operations_account_fkey
    FOREIGN KEY (account_id, user_id) REFERENCES accounts (id, user_id);
CREATE INDEX operations_account_idx ON operations (account_id) WHERE account_id IS NOT NULL;

-- Moving money between one's own accounts is neither an expense nor an income.
CREATE TABLE transfers (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT        NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    from_id     BIGINT        NOT NULL,
    to_id       BIGINT        NOT NULL,
    amount_from NUMERIC(14,2) NOT NULL CHECK (amount_from > 0),
    amount_to   NUMERIC(14,2) NOT NULL CHECK (amount_to > 0),
    occurred_on DATE          NOT NULL,
    comment     TEXT,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now(),
    CHECK (from_id <> to_id),
    FOREIGN KEY (from_id, user_id) REFERENCES accounts (id, user_id),
    FOREIGN KEY (to_id, user_id) REFERENCES accounts (id, user_id)
);
CREATE INDEX transfers_user_idx ON transfers (user_id, occurred_on DESC);

ALTER TABLE goals ADD CONSTRAINT goals_id_user_key UNIQUE (id, user_id);
CREATE TABLE tasks (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    title      TEXT        NOT NULL,
    due_on     DATE        NOT NULL,
    due_time   TEXT,
    repeat     TEXT        NOT NULL DEFAULT 'none',
    goal_id    BIGINT,
    done_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (goal_id, user_id) REFERENCES goals (id, user_id) ON DELETE SET NULL (goal_id)
);
CREATE INDEX tasks_user_idx ON tasks (user_id, due_on);
CREATE INDEX tasks_open_idx ON tasks (due_on) WHERE done_at IS NULL;

-- +goose Down
DROP TABLE tasks;
ALTER TABLE goals DROP CONSTRAINT goals_id_user_key;
DROP TABLE transfers;
DROP INDEX operations_account_idx;
ALTER TABLE operations DROP CONSTRAINT operations_account_fkey;
ALTER TABLE operations
    DROP COLUMN account_id,
    DROP COLUMN original_amount,
    DROP COLUMN currency;
DROP TABLE accounts;
