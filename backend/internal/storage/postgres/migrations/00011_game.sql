-- +goose Up
-- The game layer. The catalog of quests and challenges lives in YAML in the
-- repository, so here are only the facts about people: a new quest is a
-- commit, not a migration.
ALTER TABLE users
    ADD COLUMN xp              INTEGER  NOT NULL DEFAULT 0,
    ADD COLUMN level           SMALLINT NOT NULL DEFAULT 0 CHECK (level BETWEEN 0 AND 5),
    ADD COLUMN level_tested    BOOLEAN  NOT NULL DEFAULT false,
    ADD COLUMN streak_current  INTEGER  NOT NULL DEFAULT 0,
    ADD COLUMN streak_best     INTEGER  NOT NULL DEFAULT 0,
    ADD COLUMN streak_last_day DATE,
    ADD COLUMN streak_reminder BOOLEAN  NOT NULL DEFAULT true,
    ADD COLUMN referral_code   TEXT UNIQUE;

-- The key is what keeps experience from being given twice.
CREATE TABLE xp_log (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    amount     INTEGER     NOT NULL,
    reason     TEXT        NOT NULL,
    ref        TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, reason, ref)
);

CREATE TABLE user_quests (
    user_id    BIGINT      NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    slug       TEXT        NOT NULL,
    period_key TEXT        NOT NULL,
    status     TEXT        NOT NULL DEFAULT 'offered',
    done_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, slug, period_key)
);
CREATE INDEX user_quests_open_idx ON user_quests (status) WHERE status = 'offered';

CREATE TABLE challenge_participants (
    slug       TEXT        NOT NULL,
    user_id    BIGINT      NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    started_on DATE        NOT NULL,
    status     TEXT        NOT NULL DEFAULT 'active',
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (slug, user_id)
);

CREATE TABLE challenge_progress (
    slug    TEXT     NOT NULL,
    user_id BIGINT   NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    day_no  SMALLINT NOT NULL,
    date    DATE     NOT NULL,
    status  TEXT     NOT NULL,
    PRIMARY KEY (slug, user_id, day_no)
);
CREATE INDEX challenge_progress_date_idx ON challenge_progress (slug, date);

CREATE TABLE referrals (
    user_id     BIGINT      PRIMARY KEY REFERENCES users (telegram_id) ON DELETE CASCADE,
    referrer_id BIGINT      NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Requests for an anonymous review of someone's numbers in a video.
CREATE TABLE review_requests (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    answers    JSONB       NOT NULL,
    consent_at TIMESTAMPTZ NOT NULL,
    status     TEXT        NOT NULL DEFAULT 'new',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE review_requests;
DROP TABLE referrals;
DROP TABLE challenge_progress;
DROP TABLE challenge_participants;
DROP TABLE user_quests;
DROP TABLE xp_log;
ALTER TABLE users
    DROP COLUMN referral_code,
    DROP COLUMN streak_reminder,
    DROP COLUMN streak_last_day,
    DROP COLUMN streak_best,
    DROP COLUMN streak_current,
    DROP COLUMN level_tested,
    DROP COLUMN level,
    DROP COLUMN xp;
