-- +goose Up
-- Dialog history of the bot: one row per turn (the user message and everything
-- the model and tools produced for it), so it survives restarts.
CREATE TABLE chat_turns (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    messages   JSONB       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX chat_turns_user_idx ON chat_turns (user_id, id DESC);
CREATE INDEX chat_turns_created_at_idx ON chat_turns (created_at); -- retention cleanup

-- +goose Down
DROP TABLE chat_turns;
