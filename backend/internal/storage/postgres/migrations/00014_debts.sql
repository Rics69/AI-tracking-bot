-- +goose Up
-- principal is the balance when the debt was entered; a payment lowers it.
-- Interest the bank adds is not guessed: the balance is corrected by hand, and
-- the payoff plan models interest from the balance as it stands.
CREATE TABLE debts (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT        NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    name        TEXT          NOT NULL,
    kind        TEXT          NOT NULL DEFAULT 'loan',
    principal   NUMERIC(14,2) NOT NULL,
    rate_apr    NUMERIC(6,3)  NOT NULL DEFAULT 0 CHECK (rate_apr >= 0),
    min_payment NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (min_payment >= 0),
    due_day     SMALLINT CHECK (due_day BETWEEN 1 AND 31),
    started_on  DATE          NOT NULL,
    closed_at   TIMESTAMPTZ,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);
CREATE INDEX debts_user_idx ON debts (user_id);

-- A payment is real money leaving: it is an expense as well, kept in operations.
-- They are one fact: an expense deleted from the history takes its payment along.
CREATE TABLE debt_payments (
    id           BIGSERIAL PRIMARY KEY,
    debt_id      BIGINT        NOT NULL REFERENCES debts (id) ON DELETE CASCADE,
    user_id      BIGINT        NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    amount       NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    paid_on      DATE          NOT NULL,
    operation_id BIGINT        REFERENCES operations (id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT now()
);
CREATE INDEX debt_payments_debt_idx ON debt_payments (debt_id);

CREATE TABLE net_worth_snapshots (
    user_id BIGINT        NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    month   DATE          NOT NULL,
    assets  NUMERIC(16,2) NOT NULL,
    debts   NUMERIC(16,2) NOT NULL,
    PRIMARY KEY (user_id, month)
);

-- +goose Down
DROP TABLE net_worth_snapshots;
DROP TABLE debt_payments;
DROP TABLE debts;
