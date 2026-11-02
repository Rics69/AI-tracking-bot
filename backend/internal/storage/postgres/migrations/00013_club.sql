-- +goose Up
-- What a user has access to, and until when. The source says where it came
-- from: payments through the site will be one more source, nothing else changes.
CREATE TABLE entitlements (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    product    TEXT        NOT NULL,
    source     TEXT        NOT NULL,
    starts_at  TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX entitlements_user_idx ON entitlements (user_id, product, expires_at);

-- Only the hash of a code is stored, like invite codes.
CREATE TABLE promo_codes (
    code_hash  BYTEA PRIMARY KEY,
    product    TEXT        NOT NULL,
    days       INTEGER     NOT NULL CHECK (days > 0),
    max_uses   INTEGER     NOT NULL CHECK (max_uses > 0),
    used       INTEGER     NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE promo_uses (
    code_hash BYTEA  NOT NULL REFERENCES promo_codes (code_hash) ON DELETE CASCADE,
    user_id   BIGINT NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    PRIMARY KEY (code_hash, user_id)
);

-- +goose Down
DROP TABLE promo_uses;
DROP TABLE promo_codes;
DROP TABLE entitlements;
