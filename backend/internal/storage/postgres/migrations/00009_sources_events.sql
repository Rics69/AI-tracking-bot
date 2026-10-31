-- +goose Up
-- Where people come from: the start parameter of a link in the content.
-- pending_start keeps it until the data policy is accepted: a callback button
-- holds only 64 bytes, too little for "consent:" and a full parameter.
ALTER TABLE users
    ADD COLUMN first_source  TEXT,
    ADD COLUMN last_source   TEXT,
    ADD COLUMN pending_start TEXT;

-- What people do, for /stats. user_id is empty only for a /start before the
-- data policy is accepted: nothing about a person is stored until then.
CREATE TABLE events (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT REFERENCES users (telegram_id) ON DELETE CASCADE,
    name       TEXT        NOT NULL,
    props      JSONB       NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX events_name_time_idx ON events (name, created_at);
CREATE INDEX events_user_idx ON events (user_id);

-- Everyone who is already here came before links had labels.
UPDATE users SET first_source = 'direct', last_source = 'direct' WHERE consented_at IS NOT NULL;

-- +goose Down
DROP TABLE events;
ALTER TABLE users
    DROP COLUMN pending_start,
    DROP COLUMN last_source,
    DROP COLUMN first_source;
