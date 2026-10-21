-- +goose Up
-- The bot opens to everyone: each user gets a family of their own, and a
-- family is joined only by invite. Expenses belong to the person; the family
-- only decides who sees them (the head sees every member's expenses).
CREATE TABLE families (
    id         BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE users
    ADD COLUMN family_id    BIGINT REFERENCES families (id),
    ADD COLUMN timezone     TEXT,        -- IANA name; NULL means the app default (APP_TIMEZONE)
    ADD COLUMN consented_at TIMESTAMPTZ, -- accepted the data policy; NULL: the bot asks first
    ADD COLUMN blocked_at   TIMESTAMPTZ, -- banned for abuse
    ADD COLUMN last_seen_at TIMESTAMPTZ;

ALTER TABLE invites
    ADD COLUMN family_id  BIGINT REFERENCES families (id) ON DELETE CASCADE,
    ADD COLUMN created_by BIGINT REFERENCES users (telegram_id) ON DELETE SET NULL;

-- Until now the database held one family: its active members stay together
-- with one head, removed members get families of their own and keep their
-- expenses. Existing users accept the data policy again.
-- +goose StatementBegin
DO $$
DECLARE
    fam BIGINT;
    u   RECORD;
BEGIN
    IF EXISTS (SELECT 1 FROM users WHERE removed_at IS NULL) THEN
        INSERT INTO families DEFAULT VALUES RETURNING id INTO fam;
        UPDATE users SET family_id = fam WHERE removed_at IS NULL;
        UPDATE invites SET family_id = fam;
        -- Exactly one head: the earliest head, or the earliest member if there is none.
        UPDATE users SET is_head = (telegram_id = (
            SELECT telegram_id FROM users WHERE family_id = fam
            ORDER BY is_head DESC, created_at, telegram_id LIMIT 1))
        WHERE family_id = fam;
    END IF;
    FOR u IN SELECT telegram_id FROM users WHERE removed_at IS NOT NULL LOOP
        INSERT INTO families DEFAULT VALUES RETURNING id INTO fam;
        UPDATE users SET family_id = fam, is_head = true WHERE telegram_id = u.telegram_id;
    END LOOP;
END $$;
-- +goose StatementEnd

DELETE FROM invites WHERE family_id IS NULL;
ALTER TABLE invites ALTER COLUMN family_id SET NOT NULL;
ALTER TABLE users ALTER COLUMN family_id SET NOT NULL, DROP COLUMN removed_at;

-- Family scope: user_id IN (SELECT telegram_id FROM users WHERE family_id = $1).
CREATE INDEX users_family_idx ON users (family_id);
CREATE UNIQUE INDEX users_one_head_per_family_idx ON users (family_id) WHERE is_head;
CREATE INDEX invites_family_idx ON invites (family_id, created_at DESC);

-- Daily usage of the LLM per user, in the user's calendar day: limits and owner stats.
CREATE TABLE llm_usage (
    user_id           BIGINT NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    day               DATE   NOT NULL,
    requests          INT    NOT NULL DEFAULT 0,
    failures          INT    NOT NULL DEFAULT 0,
    prompt_tokens     BIGINT NOT NULL DEFAULT 0,
    cached_tokens     BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
);

-- +goose Down
DROP TABLE llm_usage;
DROP INDEX invites_family_idx;
ALTER TABLE invites DROP COLUMN created_by, DROP COLUMN family_id;
ALTER TABLE users
    ADD COLUMN removed_at TIMESTAMPTZ,
    DROP COLUMN last_seen_at,
    DROP COLUMN blocked_at,
    DROP COLUMN consented_at,
    DROP COLUMN timezone,
    DROP COLUMN family_id;
DROP TABLE families;
