// Package game is the reason to come back: quests, experience, the level of
// the character, streaks of days and challenges. It gives no money and does
// not touch any: it only looks at the ledger and the budget.
package game

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

// MaxLevel is the top of the ladder of the character.
const MaxLevel = 5

const (
	noRepeatDays = 14 // a quest of the day does not come back for this long
	onceInList   = 3  // one-off quests shown at a time

	levelOneDays    = 10 // days with records out of levelOneWindow
	levelOneWindow  = 14
	cushionMonths   = 3
	depositsMonths  = 3
	expenseMonths   = 3 // months the average expense is taken over
	referralXP      = 50
	referralCodeLen = 6 // random bytes
)

// Quest statuses.
const (
	StatusOffered = "offered"
	StatusDone    = "done"
	StatusMissed  = "missed"
)

// Streak milestones and the experience for them.
var streakMilestones = map[int]int{7: 20, 30: 50, 100: 100}

// Experience reasons: with the ref they make a key that is given only once.
const (
	reasonQuest     = "quest"
	reasonStreak    = "streak"
	reasonChallenge = "challenge"
	reasonFinish    = "finish"
	reasonReferral  = "referral"
	reasonFreeze    = "freeze" // no experience: the key keeps it to once a week
)

var (
	// ErrUnknown: no such quest or challenge in the catalog.
	ErrUnknown = errors.New("unknown quest or challenge")
	// ErrClosed: the challenge does not take new people.
	ErrClosed = errors.New("challenge is closed")
	// ErrClubOnly: the challenge is for the club.
	ErrClubOnly = errors.New("challenge is for the club")
)

// Profile is the state of a player.
type Profile struct {
	XP           int
	Level        int
	LevelTested  bool
	Streak       int // days in a row, as of LastDay
	BestStreak   int
	LastDay      *time.Time
	ReferralCode string
}

// CurrentStreak is the streak on day: it is over once a day has been missed.
func (p Profile) CurrentStreak(day time.Time) int {
	if p.LastDay == nil || p.LastDay.Before(day.AddDate(0, 0, -1)) {
		return 0
	}
	return p.Streak
}

// QuestRow is a quest given to a user.
type QuestRow struct {
	UserID int64
	Slug   string
	Period string
	Status string
	DoneAt *time.Time
}

// Participant is a user in a challenge.
type Participant struct {
	Slug      string
	UserID    int64
	StartedOn time.Time
	Status    string
	DoneDays  int
}

// Participant statuses.
const (
	ParticipantActive   = "active"
	ParticipantFinished = "finished"
	ParticipantLeft     = "left"
)

// Day statuses of a challenge.
const (
	DayDone    = "done"
	DaySkipped = "skipped"
	DayLater   = "later"
)

// DayMark is what happened on a day of a challenge.
type DayMark struct {
	Slug   string
	UserID int64
	DayNo  int
	Date   time.Time
	Status string
}

// ChallengeStats is the owner's view of a challenge: counts, never names.
type ChallengeStats struct {
	Participants int
	Finished     int
	DoneByDay    map[int]int
}

// Review is a request for an anonymous review.
type Review struct {
	ID        int64
	Answers   map[string]string
	ConsentAt time.Time
	Status    string
	CreatedAt time.Time
}

// Store persists the game.
type Store interface {
	Profile(ctx context.Context, userID int64) (Profile, error)
	// RaiseLevel sets the level unless it is higher already.
	RaiseLevel(ctx context.Context, userID int64, level int, tested bool) error
	// AddXP gives experience once per (reason, ref).
	AddXP(ctx context.Context, userID int64, amount int, reason, ref string) (bool, error)
	// TouchStreak counts day as active; advanced is false if it already was.
	TouchStreak(ctx context.Context, userID int64, day time.Time) (Profile, bool, error)
	// SetReferralCode stores a code unless the user has one; it returns the code in force.
	SetReferralCode(ctx context.Context, userID int64, code string) (string, error)
	ReferrerOf(ctx context.Context, code string) (int64, error) // 0: nobody
	// Rewarded counts the friends a user has been rewarded for.
	Rewarded(ctx context.Context, referrerID int64) (int, error)
	AddReferral(ctx context.Context, referrerID, userID int64) error
	Referrer(ctx context.Context, userID int64) (int64, error) // 0: nobody
	// ActiveSince lists the moment of the latest operation of those who
	// recorded something after since.
	ActiveSince(ctx context.Context, since time.Time) (map[int64]time.Time, error)

	UserQuests(ctx context.Context, userID int64) ([]QuestRow, error)
	OpenQuests(ctx context.Context) ([]QuestRow, error)
	OfferQuest(ctx context.Context, userID int64, slug, period string) error
	// CloseQuest moves an offered quest to a status; false if it was not offered.
	CloseQuest(ctx context.Context, userID int64, slug, period, status string, at time.Time) (bool, error)

	Join(ctx context.Context, slug string, userID int64, startedOn time.Time) (bool, error)
	Participants(ctx context.Context) ([]Participant, error) // active ones of all challenges
	Participations(ctx context.Context, userID int64) ([]Participant, error)
	SetParticipant(ctx context.Context, slug string, userID int64, status string) error
	// MarkDay records a day; a done day stays done.
	MarkDay(ctx context.Context, m DayMark) (bool, error)
	DayMarks(ctx context.Context, slug string, userID int64) ([]DayMark, error)
	LaterMarks(ctx context.Context, date time.Time) ([]DayMark, error)
	// DayShare counts who did a task on date, of everyone active in the challenge.
	DayShare(ctx context.Context, slug string, date time.Time) (done, total int, err error)
	ChallengeStats(ctx context.Context, slug string) (ChallengeStats, error)

	AddReview(ctx context.Context, userID int64, answers map[string]string, consentAt time.Time) error
	Reviews(ctx context.Context) ([]Review, error)
	MarkReviewsSeen(ctx context.Context) error
}

// Ledger is the part of ledger.Service the checks read.
type Ledger interface {
	List(ctx context.Context, s ledger.Scope, f ledger.Filter) (ledger.Page, error)
	Summary(ctx context.Context, s ledger.Scope, f ledger.Filter) (ledger.Summary, error)
}

// Budgets is the part of budget.Service the checks read.
type Budgets interface {
	Budget(ctx context.Context, userID int64, today time.Time) (*budget.Budget, error)
	Goals(ctx context.Context, userID int64) ([]budget.Goal, error)
	Saved(ctx context.Context, userID int64, from, to time.Time) (decimal.Decimal, error)
	DaysUnderLimit(ctx context.Context, userID int64, from, to time.Time) (under, counted int, err error)
}

// Action is a button of a message the bot starts itself. Data is read by the
// telegram package.
type Action struct {
	Label string
	Data  string
}

// Data of the buttons.
const (
	ActQuest    = "qd"      // :quest slug
	ActDay      = "cd"      // :challenge slug:day number:status
	ActNoSpend  = "nospend" // today counts for the streak
	ActGoalCard = "cardg"   // :goal ID, the card with amounts
	ActJoin     = "chj"     // :challenge slug
	ActLeave    = "chl"     // :challenge slug
	ActLevelUp  = "lvlup"   // :level the user vouches for
	ActTaskDone = "taskd"   // :task ID
)

// Features of the club the game asks about.
const (
	FeatureClubChallenges = "club_challenges"
	FeatureStreakFreeze   = "streak_freeze"
)

// Access tells what the club opens; *access.Service implements it.
type Access interface {
	Has(ctx context.Context, userID int64, feature string) bool
}

// Service is the rules of the game.
type Service struct {
	catalog Catalog
	store   Store
	ledger  Ledger
	budgets Budgets
	// Access opens club challenges and the streak freeze; nil: nobody has them.
	Access Access
}

func NewService(c Catalog, store Store, l Ledger, b Budgets) *Service {
	return &Service{catalog: c, store: store, ledger: l, budgets: b}
}

// Catalog is the content in force.
func (s *Service) Catalog() Catalog { return s.catalog }

func (s *Service) Profile(ctx context.Context, userID int64) (Profile, error) {
	return s.store.Profile(ctx, userID)
}

// Quests.

// Done is what a finished task gave.
type Done struct {
	XP      int
	Title   string
	Awarded bool
}

// TodayQuest returns the quest of the day, choosing it on the first call of
// the day, and its status.
func (s *Service) TodayQuest(ctx context.Context, userID int64, day time.Time) (Quest, string, error) {
	rows, err := s.store.UserQuests(ctx, userID)
	if err != nil {
		return Quest{}, "", err
	}
	period := day.Format(time.DateOnly)
	for _, r := range rows {
		if q, ok := s.catalog.Quest(r.Slug); ok && q.Kind == KindDaily && r.Period == period {
			return q, r.Status, nil
		}
	}
	p, err := s.store.Profile(ctx, userID)
	if err != nil {
		return Quest{}, "", err
	}
	q, ok := s.pickDaily(userID, p.Level, rows, day)
	if !ok {
		return Quest{}, "", ErrUnknown
	}
	if err := s.store.OfferQuest(ctx, userID, q.Slug, period); err != nil {
		return Quest{}, "", err
	}
	return q, StatusOffered, nil
}

// pickDaily chooses among the quests of the level, those not seen lately
// first. The choice depends on the user and the day: repeatable, and not the
// same for everyone.
func (s *Service) pickDaily(userID int64, level int, rows []QuestRow, day time.Time) (Quest, bool) {
	since := day.AddDate(0, 0, -noRepeatDays).Format(time.DateOnly)
	recent := map[string]bool{}
	for _, r := range rows {
		if r.Period >= since {
			recent[r.Slug] = true
		}
	}
	var fresh, all []Quest
	for _, q := range s.catalog.Quests {
		if q.Kind != KindDaily || !q.For(level) {
			continue
		}
		all = append(all, q)
		if !recent[q.Slug] {
			fresh = append(fresh, q)
		}
	}
	if len(fresh) > 0 {
		all = fresh
	}
	if len(all) == 0 {
		return Quest{}, false
	}
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%d:%s", userID, day.Format(time.DateOnly))
	return all[int(h.Sum32()%uint32(len(all)))], true
}

// Listed is a quest with its state, for /quests.
type Listed struct {
	Quest  Quest
	Period string
	Status string
}

// Quests lists what the user can do now: the quest of the day and the
// one-off and weekly quests of their level still open.
func (s *Service) Quests(ctx context.Context, userID int64, day time.Time) ([]Listed, error) {
	daily, status, err := s.TodayQuest(ctx, userID, day)
	if err != nil && !errors.Is(err, ErrUnknown) {
		return nil, err
	}
	var out []Listed
	if err == nil {
		out = append(out, Listed{Quest: daily, Period: daily.Period(day), Status: status})
	}
	p, err := s.store.Profile(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.UserQuests(ctx, userID)
	if err != nil {
		return nil, err
	}
	closed := map[string]bool{}
	for _, r := range rows {
		if r.Status != StatusOffered {
			closed[r.Slug+"/"+r.Period] = true
		}
	}
	shown := 0
	for _, q := range s.catalog.Quests {
		if q.Kind == KindDaily || !q.For(p.Level) || closed[q.Slug+"/"+q.Period(day)] || shown == onceInList {
			continue
		}
		out = append(out, Listed{Quest: q, Period: q.Period(day), Status: StatusOffered})
		shown++
	}
	return out, nil
}

// CompleteManual takes the user's word for a quest. A forged button finds
// nothing to complete.
func (s *Service) CompleteManual(ctx context.Context, userID int64, slug string, day time.Time, at time.Time) (Done, error) {
	q, ok := s.catalog.Quest(slug)
	if !ok || q.Check != CheckManual {
		return Done{}, ErrUnknown
	}
	period := q.Period(day)
	if err := s.store.OfferQuest(ctx, userID, slug, period); err != nil {
		return Done{}, err
	}
	return s.complete(ctx, userID, q, period, day, at)
}

func (s *Service) complete(ctx context.Context, userID int64, q Quest, period string, day, at time.Time) (Done, error) {
	closed, err := s.store.CloseQuest(ctx, userID, q.Slug, period, StatusDone, at)
	if err != nil || !closed {
		return Done{Title: q.Title}, err
	}
	awarded, err := s.store.AddXP(ctx, userID, q.XP, reasonQuest, q.Slug+":"+period)
	if err != nil {
		return Done{}, err
	}
	if _, _, err := s.store.TouchStreak(ctx, userID, day); err != nil {
		return Done{}, err
	}
	return Done{XP: q.XP, Title: q.Title, Awarded: awarded}, nil
}

// Completed is an automatic quest that has just been closed.
type Completed struct {
	UserID int64
	Done
}

// CheckQuests closes what the data allows: done when the rule holds, missed
// when the day is over and it does not. today gives each user's date.
func (s *Service) CheckQuests(ctx context.Context, today func(userID int64) (time.Time, bool), at time.Time) ([]Completed, error) {
	rows, err := s.store.OpenQuests(ctx)
	if err != nil {
		return nil, err
	}
	var out []Completed
	for _, r := range rows {
		q, ok := s.catalog.Quest(r.Slug)
		if !ok || q.Check != CheckAuto {
			continue
		}
		day, ok := today(r.UserID)
		if !ok {
			continue
		}
		period, err := time.Parse(time.DateOnly, r.Period)
		if err != nil {
			continue
		}
		over := day.After(period)
		if q.Rule.AfterDay() && !over {
			continue
		}
		met, err := s.Met(ctx, r.UserID, q.Rule, period)
		if err != nil {
			return out, err
		}
		switch {
		case met:
			d, err := s.complete(ctx, r.UserID, q, r.Period, period, at)
			if err != nil {
				return out, err
			}
			if d.Awarded {
				out = append(out, Completed{UserID: r.UserID, Done: d})
			}
		case over:
			if _, err := s.store.CloseQuest(ctx, r.UserID, q.Slug, r.Period, StatusMissed, at); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// Met reports whether the data of day satisfies a rule.
func (s *Service) Met(ctx context.Context, userID int64, r Rule, day time.Time) (bool, error) {
	switch r.Type {
	case RuleOperationsLogged, RuleNoSpend:
		page, err := s.ledger.List(ctx, ledger.OwnedBy(userID), ledger.Filter{From: &day, To: &day, Limit: ledger.MaxLimit})
		if err != nil {
			return false, err
		}
		if r.Type == RuleOperationsLogged {
			return page.Total >= r.Count, nil
		}
		return noSpend(page.Items, r), nil
	case RuleGoalContribution:
		saved, err := s.budgets.Saved(ctx, userID, day, day)
		return saved.IsPositive(), err
	case RuleUnderDailyLimit:
		b, err := s.budgets.Budget(ctx, userID, day)
		if err != nil || b == nil {
			return false, err
		}
		return b.Ready && !b.Overspent(), nil
	default:
		return false, fmt.Errorf("unknown rule %q", r.Type)
	}
}

// noSpend needs something recorded that day: silence proves nothing.
func noSpend(ops []ledger.Operation, r Rule) bool {
	for _, o := range ops {
		if r.Category != "" && o.Category != r.Category {
			continue
		}
		if r.Text == "" || strings.Contains(strings.ToLower(o.Comment), r.Text) {
			return false
		}
	}
	return len(ops) > 0
}

// Streaks.

// Milestone is a streak that has just reached a mark.
type Milestone struct {
	Days int
	XP   int
}

func (s *Service) has(ctx context.Context, userID int64, feature string) bool {
	return s.Access != nil && s.Access.Has(ctx, userID, feature)
}

// freeze covers one missed day of a streak, once a week: a privilege of the
// club. It counts as the day before, so the streak goes on.
func (s *Service) freeze(ctx context.Context, userID int64, day time.Time) error {
	p, err := s.store.Profile(ctx, userID)
	if err != nil || p.LastDay == nil || !p.LastDay.Equal(day.AddDate(0, 0, -2)) || !s.has(ctx, userID, FeatureStreakFreeze) {
		return err
	}
	year, week := day.ISOWeek()
	used, err := s.store.AddXP(ctx, userID, 0, reasonFreeze, fmt.Sprintf("%d-%d", year, week))
	if err != nil || !used {
		return err
	}
	_, _, err = s.store.TouchStreak(ctx, userID, day.AddDate(0, 0, -1))
	return err
}

// Touch counts day as active and reports a milestone reached by it.
func (s *Service) Touch(ctx context.Context, userID int64, day time.Time) (*Milestone, error) {
	if err := s.freeze(ctx, userID, day); err != nil {
		return nil, err
	}
	p, advanced, err := s.store.TouchStreak(ctx, userID, day)
	if err != nil || !advanced {
		return nil, err
	}
	xp, ok := streakMilestones[p.Streak]
	if !ok {
		return nil, nil
	}
	awarded, err := s.store.AddXP(ctx, userID, xp, reasonStreak, strconv.Itoa(p.Streak)+":"+day.Format(time.DateOnly))
	if err != nil || !awarded {
		return nil, err
	}
	return &Milestone{Days: p.Streak, XP: xp}, nil
}

// ActiveSince lists who recorded something after since.
func (s *Service) ActiveSince(ctx context.Context, since time.Time) (map[int64]time.Time, error) {
	return s.store.ActiveSince(ctx, since)
}

// Levels.

// ApplyTest stores the result of the /level test; the level never goes down.
func (s *Service) ApplyTest(ctx context.Context, userID int64, level int) (Profile, error) {
	if err := s.store.RaiseLevel(ctx, userID, min(level, MaxLevel), true); err != nil {
		return Profile{}, err
	}
	return s.store.Profile(ctx, userID)
}

// SelfConfirmed reports the levels a person vouches for: income and freedom
// are not in the data.
func SelfConfirmed(level int) bool { return level == 3 || level == 5 }

// Confirm raises the level by the user's word; only the next one, and only
// where the rules ask for it.
func (s *Service) Confirm(ctx context.Context, userID int64, level int) (bool, error) {
	p, err := s.store.Profile(ctx, userID)
	if err != nil {
		return false, err
	}
	if !SelfConfirmed(level) || p.Level+1 != level {
		return false, nil
	}
	return true, s.store.RaiseLevel(ctx, userID, level, p.LevelTested)
}

// CheckLevel raises the level if the data says the next one is reached.
func (s *Service) CheckLevel(ctx context.Context, userID int64, day time.Time) (int, bool, error) {
	p, err := s.store.Profile(ctx, userID)
	if err != nil {
		return 0, false, err
	}
	next := p.Level + 1
	var reached bool
	switch next {
	case 1:
		reached, err = s.recordsEnough(ctx, userID, day)
	case 2:
		reached, err = s.cushionEnough(ctx, userID, day)
	case 4:
		reached, err = s.depositsEnough(ctx, userID, day)
	}
	if err != nil || !reached {
		return p.Level, false, err
	}
	return next, true, s.store.RaiseLevel(ctx, userID, next, p.LevelTested)
}

func (s *Service) recordsEnough(ctx context.Context, userID int64, day time.Time) (bool, error) {
	from := day.AddDate(0, 0, 1-levelOneWindow)
	page, err := s.ledger.List(ctx, ledger.OwnedBy(userID), ledger.Filter{From: &from, To: &day, Limit: ledger.MaxLimit})
	if err != nil {
		return false, err
	}
	days := map[time.Time]bool{}
	for _, o := range page.Items {
		days[o.Date] = true
	}
	return len(days) >= levelOneDays, nil
}

func monthStart(day time.Time, back int) time.Time {
	return time.Date(day.Year(), day.Month()-time.Month(back), 1, 0, 0, 0, 0, time.UTC)
}

// cushionEnough compares the savings of the cushion with three months of the
// average expense over the last full months.
func (s *Service) cushionEnough(ctx context.Context, userID int64, day time.Time) (bool, error) {
	goals, err := s.budgets.Goals(ctx, userID)
	if err != nil {
		return false, err
	}
	saved := decimal.Zero
	for _, g := range goals {
		if g.IsCushion() {
			saved = saved.Add(g.Saved)
		}
	}
	if !saved.IsPositive() {
		return false, nil
	}
	from, to := monthStart(day, expenseMonths), monthStart(day, 0).AddDate(0, 0, -1)
	sum, err := s.ledger.Summary(ctx, ledger.OwnedBy(userID), ledger.Filter{From: &from, To: &to})
	if err != nil {
		return false, err
	}
	if sum.Expense.Count == 0 {
		return false, nil
	}
	monthly := sum.Expense.Sum.Div(decimal.NewFromInt(expenseMonths))
	return saved.GreaterThanOrEqual(monthly.Mul(decimal.NewFromInt(cushionMonths))), nil
}

// depositsEnough wants money put aside in each of the last full months.
func (s *Service) depositsEnough(ctx context.Context, userID int64, day time.Time) (bool, error) {
	for back := 1; back <= depositsMonths; back++ {
		from, to := monthStart(day, back), monthStart(day, back-1).AddDate(0, 0, -1)
		saved, err := s.budgets.Saved(ctx, userID, from, to)
		if err != nil {
			return false, err
		}
		if !saved.IsPositive() {
			return false, nil
		}
	}
	return true, nil
}

// Referrals.

// ReferralCode returns the user's code, creating it on the first call.
func (s *Service) ReferralCode(ctx context.Context, userID int64) (string, error) {
	p, err := s.store.Profile(ctx, userID)
	if err != nil {
		return "", err
	}
	if p.ReferralCode != "" {
		return p.ReferralCode, nil
	}
	b := make([]byte, referralCodeLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return s.store.SetReferralCode(ctx, userID, base64.RawURLEncoding.EncodeToString(b))
}

// Refer notes who brought a new user; one's own link does not count.
func (s *Service) Refer(ctx context.Context, code string, userID int64) error {
	referrer, err := s.store.ReferrerOf(ctx, code)
	if err != nil || referrer == 0 || referrer == userID {
		return err
	}
	return s.store.AddReferral(ctx, referrer, userID)
}

// RewardReferrer gives the experience once the new user has finished
// onboarding. It returns who got it, 0 if nobody, and how many friends they
// have brought so far.
func (s *Service) RewardReferrer(ctx context.Context, userID int64) (int64, int, error) {
	referrer, err := s.store.Referrer(ctx, userID)
	if err != nil || referrer == 0 {
		return 0, 0, err
	}
	awarded, err := s.store.AddXP(ctx, referrer, referralXP, reasonReferral, strconv.FormatInt(userID, 10))
	if err != nil || !awarded {
		return 0, 0, err
	}
	friends, err := s.store.Rewarded(ctx, referrer)
	return referrer, friends, err
}

// Challenges.

// Current is the challenge the user is in, with today's task.
type Current struct {
	Challenge Challenge
	Part      Participant
	DayNo     int  // 1-based; 0 before the start
	Over      bool // all the days are behind
	Today     *ChallengeDay
	Mark      string // of today, if any
}

// Join enters a challenge.
func (s *Service) Join(ctx context.Context, slug string, userID int64, day time.Time) (Challenge, bool, error) {
	c, ok := s.catalog.Challenge(slug)
	if !ok {
		return Challenge{}, false, ErrUnknown
	}
	if !c.Open(day) {
		return c, false, ErrClosed
	}
	if c.Access == AccessClub && !s.has(ctx, userID, FeatureClubChallenges) {
		return c, false, ErrClubOnly
	}
	joined, err := s.store.Join(ctx, slug, userID, c.StartFor(day))
	return c, joined, err
}

// Leave quits a challenge.
func (s *Service) Leave(ctx context.Context, slug string, userID int64) error {
	return s.store.SetParticipant(ctx, slug, userID, ParticipantLeft)
}

// Active lists the user's current challenges.
func (s *Service) Active(ctx context.Context, userID int64, day time.Time) ([]Current, error) {
	parts, err := s.store.Participations(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []Current
	for _, p := range parts {
		if p.Status != ParticipantActive {
			continue
		}
		c, ok := s.catalog.Challenge(p.Slug)
		if !ok {
			continue // the file was removed: nothing to show
		}
		cur, err := s.current(ctx, c, p, day)
		if err != nil {
			return nil, err
		}
		out = append(out, cur)
	}
	return out, nil
}

func (s *Service) current(ctx context.Context, c Challenge, p Participant, day time.Time) (Current, error) {
	cur := Current{Challenge: c, Part: p}
	if day.Before(p.StartedOn) {
		return cur, nil
	}
	cur.DayNo = int(day.Sub(p.StartedOn).Hours()/24+0.5) + 1
	if cur.DayNo > len(c.Days) {
		cur.Over = true
		return cur, nil
	}
	cur.Today = &c.Days[cur.DayNo-1]
	marks, err := s.store.DayMarks(ctx, c.Slug, p.UserID)
	if err != nil {
		return Current{}, err
	}
	for _, m := range marks {
		if m.DayNo == cur.DayNo {
			cur.Mark = m.Status
		}
	}
	return cur, nil
}

// MarkDay answers a button of today's task: done, later or skipped. A button
// of another day does nothing.
func (s *Service) MarkDay(ctx context.Context, slug string, userID int64, dayNo int, status string, day time.Time) (Done, error) {
	parts, err := s.Active(ctx, userID, day)
	if err != nil {
		return Done{}, err
	}
	for _, cur := range parts {
		if cur.Challenge.Slug != slug || cur.DayNo != dayNo || cur.Today == nil {
			continue
		}
		return s.mark(ctx, cur, status, day)
	}
	return Done{}, ErrUnknown
}

func (s *Service) mark(ctx context.Context, cur Current, status string, day time.Time) (Done, error) {
	userID, slug := cur.Part.UserID, cur.Challenge.Slug
	marked, err := s.store.MarkDay(ctx, DayMark{Slug: slug, UserID: userID, DayNo: cur.DayNo, Date: day, Status: status})
	if err != nil || !marked || status != DayDone {
		return Done{Title: cur.Today.Title}, err
	}
	xp := cur.Today.XP
	awarded, err := s.store.AddXP(ctx, userID, xp, reasonChallenge, slug+":"+strconv.Itoa(cur.DayNo))
	if err != nil {
		return Done{}, err
	}
	if _, _, err := s.store.TouchStreak(ctx, userID, day); err != nil {
		return Done{}, err
	}
	return Done{XP: xp, Title: cur.Today.Title, Awarded: awarded}, nil
}

// Finished is a challenge someone has reached the end of.
type Finished struct {
	Challenge Challenge
	UserID    int64
	DoneDays  int
	XP        int
	Saved     decimal.Decimal
	Under     int // days within the limit
	Counted   int // days the limit was known
}

// CheckChallenges closes automatic tasks and finishes the challenges that are
// over. today gives each user's date.
func (s *Service) CheckChallenges(ctx context.Context, today func(userID int64) (time.Time, bool)) ([]Completed, []Finished, error) {
	parts, err := s.store.Participants(ctx)
	if err != nil {
		return nil, nil, err
	}
	var done []Completed
	var finished []Finished
	for _, p := range parts {
		c, ok := s.catalog.Challenge(p.Slug)
		if !ok {
			continue
		}
		day, ok := today(p.UserID)
		if !ok {
			continue
		}
		cur, err := s.current(ctx, c, p, day)
		if err != nil {
			return done, finished, err
		}
		if cur.Over {
			f, err := s.finish(ctx, c, p, day)
			if err != nil {
				return done, finished, err
			}
			finished = append(finished, f)
			continue
		}
		if cur.Today == nil || cur.Today.Check != CheckAuto || cur.Mark == DayDone {
			continue
		}
		met, err := s.Met(ctx, p.UserID, cur.Today.Rule, day)
		if err != nil {
			return done, finished, err
		}
		if !met {
			continue
		}
		d, err := s.mark(ctx, cur, DayDone, day)
		if err != nil {
			return done, finished, err
		}
		if d.Awarded {
			done = append(done, Completed{UserID: p.UserID, Done: d})
		}
	}
	return done, finished, nil
}

func (s *Service) finish(ctx context.Context, c Challenge, p Participant, day time.Time) (Finished, error) {
	if err := s.store.SetParticipant(ctx, c.Slug, p.UserID, ParticipantFinished); err != nil {
		return Finished{}, err
	}
	f := Finished{Challenge: c, UserID: p.UserID, DoneDays: p.DoneDays}
	if c.XPFinish > 0 {
		awarded, err := s.store.AddXP(ctx, p.UserID, c.XPFinish, reasonFinish, c.Slug)
		if err != nil {
			return Finished{}, err
		}
		if awarded {
			f.XP = c.XPFinish
		}
	}
	last := day.AddDate(0, 0, -1)
	var err error
	if f.Saved, err = s.budgets.Saved(ctx, p.UserID, p.StartedOn, last); err != nil {
		return Finished{}, err
	}
	if f.Under, f.Counted, err = s.budgets.DaysUnderLimit(ctx, p.UserID, p.StartedOn, last); err != nil {
		return Finished{}, err
	}
	return f, nil
}

// YesterdayShare is the share of participants who did yesterday's task, in
// percent; ok is false with too few people to say that without naming anyone.
func (s *Service) YesterdayShare(ctx context.Context, slug string, day time.Time) (int, bool, error) {
	done, total, err := s.store.DayShare(ctx, slug, day.AddDate(0, 0, -1))
	if err != nil || total < minShareGroup {
		return 0, false, err
	}
	return done * 100 / total, true, nil
}

const minShareGroup = 3

// LaterMarks lists the tasks postponed on date.
func (s *Service) LaterMarks(ctx context.Context, date time.Time) ([]DayMark, error) {
	return s.store.LaterMarks(ctx, date)
}

// Stats is the owner's view of a challenge, with the day most people dropped at.
func (s *Service) Stats(ctx context.Context, slug string) (ChallengeStats, int, error) {
	if _, ok := s.catalog.Challenge(slug); !ok {
		return ChallengeStats{}, 0, ErrUnknown
	}
	st, err := s.store.ChallengeStats(ctx, slug)
	if err != nil {
		return ChallengeStats{}, 0, err
	}
	days := make([]int, 0, len(st.DoneByDay))
	for d := range st.DoneByDay {
		days = append(days, d)
	}
	slices.SortFunc(days, cmp.Compare[int])
	worst, drop := 0, 0
	for i := 1; i < len(days); i++ {
		if d := st.DoneByDay[days[i-1]] - st.DoneByDay[days[i]]; d > drop {
			worst, drop = days[i], d
		}
	}
	return st, worst, nil
}

// ChallengeUsers lists everyone in a running challenge: they get the morning
// message whatever else they have set up.
func (s *Service) ChallengeUsers(ctx context.Context) ([]int64, error) {
	parts, err := s.store.Participants(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(parts))
	for i, p := range parts {
		ids[i] = p.UserID
	}
	return ids, nil
}

// Reviews.

func (s *Service) AddReview(ctx context.Context, userID int64, answers map[string]string, at time.Time) error {
	return s.store.AddReview(ctx, userID, answers, at)
}

func (s *Service) Reviews(ctx context.Context) ([]Review, error) { return s.store.Reviews(ctx) }

func (s *Service) MarkReviewsSeen(ctx context.Context) error { return s.store.MarkReviewsSeen(ctx) }

// Week is the part of the weekly report that comes from the game and the goals.
type Week struct {
	Goal         *budget.Goal
	GoalAdded    decimal.Decimal // put aside for goals during the week
	DaysUnder    int
	DaysCounted  int
	Streak       int
	StreakBroken bool
	QuestsDone   int
}

// Week gathers the report's facts about a week; today is the user's date.
func (s *Service) Week(ctx context.Context, userID int64, weekStart, today time.Time) (Week, error) {
	end := weekStart.AddDate(0, 0, 6)
	var w Week
	goals, err := s.budgets.Goals(ctx, userID)
	if err != nil {
		return Week{}, err
	}
	w.Goal = budget.MainGoal(goals)
	if w.GoalAdded, err = s.budgets.Saved(ctx, userID, weekStart, end); err != nil {
		return Week{}, err
	}
	if w.DaysUnder, w.DaysCounted, err = s.budgets.DaysUnderLimit(ctx, userID, weekStart, end); err != nil {
		return Week{}, err
	}
	p, err := s.store.Profile(ctx, userID)
	if err != nil {
		return Week{}, err
	}
	w.Streak = p.CurrentStreak(today)
	w.StreakBroken = w.Streak == 0 && p.LastDay != nil && !p.LastDay.Before(weekStart.AddDate(0, 0, -1))
	rows, err := s.store.UserQuests(ctx, userID)
	if err != nil {
		return Week{}, err
	}
	for _, r := range rows {
		if r.Status == StatusDone && r.DoneAt != nil && !r.DoneAt.Before(weekStart) && r.DoneAt.Before(end.AddDate(0, 0, 1)) {
			w.QuestsDone++
		}
	}
	return w, nil
}
