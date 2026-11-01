package memory

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/game"
)

// Game implements game.Store in memory.
type Game struct {
	Operations *OperationRepository // where ActiveSince looks

	mu        sync.Mutex
	profiles  map[int64]game.Profile
	xp        map[string]bool
	quests    []game.QuestRow
	parts     []game.Participant
	marks     []game.DayMark
	referrals map[int64]int64
	reviews   []game.Review
}

var _ game.Store = (*Game)(nil)

func NewGame() *Game {
	return &Game{profiles: map[int64]game.Profile{}, xp: map[string]bool{}, referrals: map[int64]int64{}}
}

func (r *Game) Profile(_ context.Context, userID int64) (game.Profile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.profiles[userID], nil
}

func (r *Game) RaiseLevel(_ context.Context, userID int64, level int, tested bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.profiles[userID]
	p.Level = max(p.Level, level)
	p.LevelTested = p.LevelTested || tested
	r.profiles[userID] = p
	return nil
}

func (r *Game) AddXP(_ context.Context, userID int64, amount int, reason, ref string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := fmt.Sprint(userID, "|", reason, "|", ref)
	if r.xp[key] {
		return false, nil
	}
	r.xp[key] = true
	p := r.profiles[userID]
	p.XP += amount
	r.profiles[userID] = p
	return true, nil
}

func (r *Game) TouchStreak(_ context.Context, userID int64, day time.Time) (game.Profile, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.profiles[userID]
	if p.LastDay != nil && !p.LastDay.Before(day) {
		return p, false, nil
	}
	if p.LastDay != nil && p.LastDay.Equal(day.AddDate(0, 0, -1)) {
		p.Streak++
	} else {
		p.Streak = 1
	}
	p.BestStreak = max(p.BestStreak, p.Streak)
	p.LastDay = &day
	r.profiles[userID] = p
	return p, true, nil
}

func (r *Game) SetReferralCode(_ context.Context, userID int64, code string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.profiles[userID]
	if p.ReferralCode == "" {
		p.ReferralCode = code
		r.profiles[userID] = p
	}
	return p.ReferralCode, nil
}

func (r *Game) ReferrerOf(_ context.Context, code string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, p := range r.profiles {
		if p.ReferralCode == code {
			return id, nil
		}
	}
	return 0, nil
}

func (r *Game) AddReferral(_ context.Context, referrerID, userID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.referrals[userID]; !ok {
		r.referrals[userID] = referrerID
	}
	return nil
}

func (r *Game) Referrer(_ context.Context, userID int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.referrals[userID], nil
}

func (r *Game) ActiveSince(_ context.Context, since time.Time) (map[int64]time.Time, error) {
	out := map[int64]time.Time{}
	if r.Operations == nil {
		return out, nil
	}
	for _, o := range r.Operations.All() {
		if !o.CreatedAt.Before(since) && o.CreatedAt.After(out[o.UserID]) {
			out[o.UserID] = o.CreatedAt
		}
	}
	return out, nil
}

func (r *Game) UserQuests(_ context.Context, userID int64) ([]game.QuestRow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []game.QuestRow
	for _, q := range r.quests {
		if q.UserID == userID {
			out = append(out, q)
		}
	}
	return out, nil
}

func (r *Game) OpenQuests(context.Context) ([]game.QuestRow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []game.QuestRow
	for _, q := range r.quests {
		if q.Status == game.StatusOffered {
			out = append(out, q)
		}
	}
	return out, nil
}

func (r *Game) OfferQuest(_ context.Context, userID int64, slug, period string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, q := range r.quests {
		if q.UserID == userID && q.Slug == slug && q.Period == period {
			return nil
		}
	}
	r.quests = append(r.quests, game.QuestRow{UserID: userID, Slug: slug, Period: period, Status: game.StatusOffered})
	return nil
}

func (r *Game) CloseQuest(_ context.Context, userID int64, slug, period, status string, at time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, q := range r.quests {
		if q.UserID == userID && q.Slug == slug && q.Period == period && q.Status == game.StatusOffered {
			r.quests[i].Status, r.quests[i].DoneAt = status, &at
			return true, nil
		}
	}
	return false, nil
}

func (r *Game) Join(_ context.Context, slug string, userID int64, startedOn time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.parts {
		if p.Slug == slug && p.UserID == userID {
			return false, nil
		}
	}
	r.parts = append(r.parts, game.Participant{Slug: slug, UserID: userID, StartedOn: startedOn, Status: game.ParticipantActive})
	return true, nil
}

func (r *Game) withDone(p game.Participant) game.Participant {
	p.DoneDays = 0
	for _, m := range r.marks {
		if m.Slug == p.Slug && m.UserID == p.UserID && m.Status == game.DayDone {
			p.DoneDays++
		}
	}
	return p
}

func (r *Game) Participants(context.Context) ([]game.Participant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []game.Participant
	for _, p := range r.parts {
		if p.Status == game.ParticipantActive {
			out = append(out, r.withDone(p))
		}
	}
	return out, nil
}

func (r *Game) Participations(_ context.Context, userID int64) ([]game.Participant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []game.Participant
	for _, p := range r.parts {
		if p.UserID == userID {
			out = append(out, r.withDone(p))
		}
	}
	return out, nil
}

func (r *Game) SetParticipant(_ context.Context, slug string, userID int64, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, p := range r.parts {
		if p.Slug == slug && p.UserID == userID {
			r.parts[i].Status = status
		}
	}
	return nil
}

func (r *Game) MarkDay(_ context.Context, m game.DayMark) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, old := range r.marks {
		if old.Slug == m.Slug && old.UserID == m.UserID && old.DayNo == m.DayNo {
			if old.Status == game.DayDone {
				return false, nil
			}
			r.marks[i] = m
			return true, nil
		}
	}
	r.marks = append(r.marks, m)
	return true, nil
}

func (r *Game) DayMarks(_ context.Context, slug string, userID int64) ([]game.DayMark, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []game.DayMark
	for _, m := range r.marks {
		if m.Slug == slug && m.UserID == userID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *Game) LaterMarks(_ context.Context, date time.Time) ([]game.DayMark, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []game.DayMark
	for _, m := range r.marks {
		if m.Date.Equal(date) && m.Status == game.DayLater {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *Game) DayShare(_ context.Context, slug string, date time.Time) (done, total int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.marks {
		if m.Slug == slug && m.Date.Equal(date) && m.Status == game.DayDone {
			done++
		}
	}
	for _, p := range r.parts {
		if p.Slug == slug && p.Status == game.ParticipantActive {
			total++
		}
	}
	return done, total, nil
}

func (r *Game) ChallengeStats(_ context.Context, slug string) (game.ChallengeStats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := game.ChallengeStats{DoneByDay: map[int]int{}}
	for _, p := range r.parts {
		if p.Slug == slug {
			st.Participants++
			if p.Status == game.ParticipantFinished {
				st.Finished++
			}
		}
	}
	for _, m := range r.marks {
		if m.Slug == slug && m.Status == game.DayDone {
			st.DoneByDay[m.DayNo]++
		}
	}
	return st, nil
}

func (r *Game) AddReview(_ context.Context, _ int64, answers map[string]string, consentAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reviews = append(r.reviews, game.Review{ID: int64(len(r.reviews) + 1), Answers: maps.Clone(answers),
		ConsentAt: consentAt, Status: "new", CreatedAt: consentAt})
	return nil
}

func (r *Game) Reviews(context.Context) ([]game.Review, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.reviews), nil
}

func (r *Game) MarkReviewsSeen(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.reviews {
		r.reviews[i].Status = "seen"
	}
	return nil
}
