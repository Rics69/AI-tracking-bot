-- +goose Up
-- Weekly reports: members can opt out in the panel; report_runs remembers which
-- reports were sent, so a restart does not send one twice.
ALTER TABLE users ADD COLUMN weekly_report BOOLEAN NOT NULL DEFAULT true;

CREATE TABLE report_runs (
    week_start DATE        NOT NULL,
    user_id    BIGINT      NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    sent_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (week_start, user_id)
);

-- +goose Down
DROP TABLE report_runs;
ALTER TABLE users DROP COLUMN weekly_report;
