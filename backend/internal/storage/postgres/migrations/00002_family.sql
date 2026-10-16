-- +goose Up
-- Membership lives in the database instead of ALLOWED_TELEGRAM_IDS:
-- members are added by invite links and removed softly, keeping their expenses.
ALTER TABLE users
    ADD COLUMN is_head    BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN removed_at TIMESTAMPTZ;

CREATE TABLE invites (
    id         BIGSERIAL PRIMARY KEY,
    code_hash  BYTEA       NOT NULL UNIQUE, -- sha256 of the code: the code itself is never stored
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    used_by    BIGINT REFERENCES users (telegram_id) ON DELETE SET NULL,
    revoked_at TIMESTAMPTZ
);

-- Small key-value store, e.g. the bot's @username for invite links.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT        NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE settings;
DROP TABLE invites;
ALTER TABLE users DROP COLUMN removed_at, DROP COLUMN is_head;
