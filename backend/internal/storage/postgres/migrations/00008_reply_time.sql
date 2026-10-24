-- +goose Up
-- Total time of agent replies per user and day: /stats shows the average.
ALTER TABLE llm_usage ADD COLUMN reply_ms BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE llm_usage DROP COLUMN reply_ms;
