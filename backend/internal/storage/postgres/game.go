package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/game"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// GameRepository implements game.Store.
type GameRepository struct {
	pool *pgxpool.Pool
}

var _ game.Store = (*GameRepository)(nil)

func NewGameRepository(pool *pgxpool.Pool) *GameRepository {
	return &GameRepository{pool: pool}
}

const profileColumns = `xp, level, level_tested, streak_current, streak_best, streak_last_day, COALESCE(referral_code, '')`

func scanProfile(row pgx.Row) (game.Profile, error) {
	var p game.Profile
	err := row.Scan(&p.XP, &p.Level, &p.LevelTested, &p.Streak, &p.BestStreak, &p.LastDay, &p.ReferralCode)
	return p, err
}

func (r *GameRepository) Profile(ctx context.Context, userID int64) (game.Profile, error) {
	p, err := scanProfile(r.pool.QueryRow(ctx, `SELECT `+profileColumns+` FROM users WHERE telegram_id = $1`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return game.Profile{}, user.ErrNotFound
	}
	if err != nil {
		return game.Profile{}, fmt.Errorf("get profile: %w", err)
	}
	return p, nil
}

func (r *GameRepository) RaiseLevel(ctx context.Context, userID int64, level int, tested bool) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE users SET level = GREATEST(level, $2), level_tested = level_tested OR $3, updated_at = now()
		WHERE telegram_id = $1`, userID, level, tested)
	if err != nil {
		return fmt.Errorf("raise level: %w", err)
	}
	return nil
}

func (r *GameRepository) AddXP(ctx context.Context, userID int64, amount int, reason, ref string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		WITH added AS (
			INSERT INTO xp_log (user_id, amount, reason, ref) VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id, reason, ref) DO NOTHING
			RETURNING amount
		)
		UPDATE users SET xp = xp + added.amount FROM added WHERE telegram_id = $1`, userID, amount, reason, ref)
	if err != nil {
		return false, fmt.Errorf("add xp: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *GameRepository) TouchStreak(ctx context.Context, userID int64, day time.Time) (game.Profile, bool, error) {
	p, err := scanProfile(r.pool.QueryRow(ctx, `
		WITH next AS (
			SELECT CASE WHEN streak_last_day = $2::date - 1 THEN streak_current + 1 ELSE 1 END AS streak
			FROM users
			WHERE telegram_id = $1 AND (streak_last_day IS NULL OR streak_last_day < $2::date)
			FOR UPDATE
		)
		UPDATE users SET streak_current = next.streak, streak_best = GREATEST(streak_best, next.streak), streak_last_day = $2::date
		FROM next WHERE telegram_id = $1
		RETURNING `+profileColumns, userID, day))
	if errors.Is(err, pgx.ErrNoRows) {
		p, err := r.Profile(ctx, userID)
		return p, false, err
	}
	if err != nil {
		return game.Profile{}, false, fmt.Errorf("touch streak: %w", err)
	}
	return p, true, nil
}

func (r *GameRepository) SetReferralCode(ctx context.Context, userID int64, code string) (string, error) {
	var saved string
	err := r.pool.QueryRow(ctx, `
		UPDATE users SET referral_code = COALESCE(referral_code, $2) WHERE telegram_id = $1
		RETURNING referral_code`, userID, code).Scan(&saved)
	if err != nil {
		return "", fmt.Errorf("set referral code: %w", err)
	}
	return saved, nil
}

func (r *GameRepository) ReferrerOf(ctx context.Context, code string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `SELECT telegram_id FROM users WHERE referral_code = $1`, code).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("find referrer: %w", err)
	}
	return id, nil
}

func (r *GameRepository) Rewarded(ctx context.Context, referrerID int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM xp_log WHERE user_id = $1 AND reason = 'referral'`, referrerID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count referrals: %w", err)
	}
	return n, nil
}

func (r *GameRepository) AddReferral(ctx context.Context, referrerID, userID int64) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO referrals (user_id, referrer_id) VALUES ($1, $2)
		ON CONFLICT (user_id) DO NOTHING`, userID, referrerID)
	if err != nil {
		return fmt.Errorf("add referral: %w", err)
	}
	return nil
}

func (r *GameRepository) Referrer(ctx context.Context, userID int64) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `SELECT referrer_id FROM referrals WHERE user_id = $1`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get referrer: %w", err)
	}
	return id, nil
}

func (r *GameRepository) ActiveSince(ctx context.Context, since time.Time) (map[int64]time.Time, error) {
	rows, err := r.pool.Query(ctx, `SELECT user_id, max(created_at) FROM operations WHERE created_at >= $1 GROUP BY user_id`, since)
	if err != nil {
		return nil, fmt.Errorf("list active users: %w", err)
	}
	defer rows.Close()
	out := map[int64]time.Time{}
	for rows.Next() {
		var (
			id int64
			at time.Time
		)
		if err := rows.Scan(&id, &at); err != nil {
			return nil, fmt.Errorf("scan active users: %w", err)
		}
		out[id] = at
	}
	return out, rows.Err()
}

func (r *GameRepository) questRows(ctx context.Context, where string, args ...any) ([]game.QuestRow, error) {
	rows, err := r.pool.Query(ctx, `SELECT user_id, slug, period_key, status, done_at FROM user_quests WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list quests: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[game.QuestRow])
	if err != nil {
		return nil, fmt.Errorf("list quests: %w", err)
	}
	return out, nil
}

func (r *GameRepository) UserQuests(ctx context.Context, userID int64) ([]game.QuestRow, error) {
	return r.questRows(ctx, `user_id = $1 ORDER BY created_at`, userID)
}

func (r *GameRepository) OpenQuests(ctx context.Context) ([]game.QuestRow, error) {
	return r.questRows(ctx, `status = $1`, game.StatusOffered)
}

func (r *GameRepository) OfferQuest(ctx context.Context, userID int64, slug, period string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO user_quests (user_id, slug, period_key) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, userID, slug, period)
	if err != nil {
		return fmt.Errorf("offer quest: %w", err)
	}
	return nil
}

func (r *GameRepository) CloseQuest(ctx context.Context, userID int64, slug, period, status string, at time.Time) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE user_quests SET status = $4, done_at = $5
		WHERE user_id = $1 AND slug = $2 AND period_key = $3 AND status = 'offered'`, userID, slug, period, status, at)
	if err != nil {
		return false, fmt.Errorf("close quest: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *GameRepository) Join(ctx context.Context, slug string, userID int64, startedOn time.Time) (bool, error) {
	tag, err := r.pool.Exec(ctx, `INSERT INTO challenge_participants (slug, user_id, started_on) VALUES ($1, $2, $3)
		ON CONFLICT (slug, user_id) DO NOTHING`, slug, userID, startedOn)
	if err != nil {
		return false, fmt.Errorf("join challenge: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const participantSelect = `
	SELECT p.slug, p.user_id, p.started_on, p.status,
	       (SELECT count(*) FROM challenge_progress g
	        WHERE g.slug = p.slug AND g.user_id = p.user_id AND g.status = 'done')
	FROM challenge_participants p WHERE `

func (r *GameRepository) participants(ctx context.Context, where string, args ...any) ([]game.Participant, error) {
	rows, err := r.pool.Query(ctx, participantSelect+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list participants: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[game.Participant])
	if err != nil {
		return nil, fmt.Errorf("list participants: %w", err)
	}
	return out, nil
}

func (r *GameRepository) Participants(ctx context.Context) ([]game.Participant, error) {
	return r.participants(ctx, `p.status = $1`, game.ParticipantActive)
}

func (r *GameRepository) Participations(ctx context.Context, userID int64) ([]game.Participant, error) {
	return r.participants(ctx, `p.user_id = $1 ORDER BY p.joined_at`, userID)
}

func (r *GameRepository) SetParticipant(ctx context.Context, slug string, userID int64, status string) error {
	_, err := r.pool.Exec(ctx, `UPDATE challenge_participants SET status = $3 WHERE slug = $1 AND user_id = $2`, slug, userID, status)
	if err != nil {
		return fmt.Errorf("update participant: %w", err)
	}
	return nil
}

func (r *GameRepository) MarkDay(ctx context.Context, m game.DayMark) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO challenge_progress AS p (slug, user_id, day_no, date, status) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (slug, user_id, day_no) DO UPDATE SET status = excluded.status, date = excluded.date
		WHERE p.status <> 'done'`, m.Slug, m.UserID, m.DayNo, m.Date, m.Status)
	if err != nil {
		return false, fmt.Errorf("mark day: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *GameRepository) marks(ctx context.Context, where string, args ...any) ([]game.DayMark, error) {
	rows, err := r.pool.Query(ctx, `SELECT slug, user_id, day_no, date, status FROM challenge_progress WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list day marks: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[game.DayMark])
	if err != nil {
		return nil, fmt.Errorf("list day marks: %w", err)
	}
	return out, nil
}

func (r *GameRepository) DayMarks(ctx context.Context, slug string, userID int64) ([]game.DayMark, error) {
	return r.marks(ctx, `slug = $1 AND user_id = $2 ORDER BY day_no`, slug, userID)
}

func (r *GameRepository) LaterMarks(ctx context.Context, date time.Time) ([]game.DayMark, error) {
	return r.marks(ctx, `date = $1 AND status = $2`, date, game.DayLater)
}

func (r *GameRepository) DayShare(ctx context.Context, slug string, date time.Time) (done, total int, err error) {
	err = r.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM challenge_progress WHERE slug = $1 AND date = $2 AND status = 'done'),
		       (SELECT count(*) FROM challenge_participants WHERE slug = $1 AND status = 'active')`, slug, date).Scan(&done, &total)
	if err != nil {
		return 0, 0, fmt.Errorf("count day share: %w", err)
	}
	return done, total, nil
}

func (r *GameRepository) ChallengeStats(ctx context.Context, slug string) (game.ChallengeStats, error) {
	st := game.ChallengeStats{DoneByDay: map[int]int{}}
	err := r.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status = 'finished')
		FROM challenge_participants WHERE slug = $1`, slug).Scan(&st.Participants, &st.Finished)
	if err != nil {
		return game.ChallengeStats{}, fmt.Errorf("count participants: %w", err)
	}
	rows, err := r.pool.Query(ctx, `SELECT day_no, count(*) FROM challenge_progress
		WHERE slug = $1 AND status = 'done' GROUP BY day_no`, slug)
	if err != nil {
		return game.ChallengeStats{}, fmt.Errorf("count days: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var day, n int
		if err := rows.Scan(&day, &n); err != nil {
			return game.ChallengeStats{}, fmt.Errorf("count days: %w", err)
		}
		st.DoneByDay[day] = n
	}
	return st, rows.Err()
}

func (r *GameRepository) AddReview(ctx context.Context, userID int64, answers map[string]string, consentAt time.Time) error {
	data, err := json.Marshal(answers)
	if err != nil {
		return fmt.Errorf("encode review: %w", err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO review_requests (user_id, answers, consent_at) VALUES ($1, $2, $3)`,
		userID, data, consentAt); err != nil {
		return fmt.Errorf("add review: %w", err)
	}
	return nil
}

func (r *GameRepository) Reviews(ctx context.Context) ([]game.Review, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, answers, consent_at, status, created_at FROM review_requests ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list reviews: %w", err)
	}
	defer rows.Close()
	var out []game.Review
	for rows.Next() {
		var (
			v    game.Review
			data []byte
		)
		if err := rows.Scan(&v.ID, &data, &v.ConsentAt, &v.Status, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan reviews: %w", err)
		}
		if err := json.Unmarshal(data, &v.Answers); err != nil {
			return nil, fmt.Errorf("decode review: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *GameRepository) MarkReviewsSeen(ctx context.Context) error {
	if _, err := r.pool.Exec(ctx, `UPDATE review_requests SET status = 'seen' WHERE status = 'new'`); err != nil {
		return fmt.Errorf("mark reviews: %w", err)
	}
	return nil
}
