package expense

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

const (
	DefaultLimit     = 50
	MaxLimit         = 500
	MaxCommentLength = 500
)

var (
	// NUMERIC(12,2) holds values below 10^10.
	maxAmount = decimal.New(1, 10)
	// Dates before this are almost certainly a parsing mistake.
	minSpentAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
)

// Repository persists expenses. Every method honours the scope:
// expenses outside it behave as if they did not exist.
type Repository interface {
	Create(ctx context.Context, e Expense) (Expense, error)
	Get(ctx context.Context, s Scope, id int64) (Expense, error)
	Update(ctx context.Context, s Scope, e Expense) (Expense, error)
	Delete(ctx context.Context, s Scope, id int64) error
	List(ctx context.Context, s Scope, f Filter) (Page, error)
	Summary(ctx context.Context, s Scope, f Filter) (Summary, error)
	// Aggregate sums expenses per bucket start and category; empty buckets are omitted.
	Aggregate(ctx context.Context, s Scope, f Filter, b Bucket) ([]BucketTotal, error)
}

// Service implements the expense use cases on top of a Repository.
type Service struct {
	repo Repository
	loc  *time.Location
	now  func() time.Time
}

// NewService creates a service. loc is the family timezone used to decide
// what "today" is.
func NewService(repo Repository, loc *time.Location) *Service {
	return &Service{repo: repo, loc: loc, now: time.Now}
}

// Today returns the current calendar date in the service timezone.
func (s *Service) Today() time.Time {
	return s.TodayIn(s.loc)
}

// TodayIn returns the current calendar date in loc.
func (s *Service) TodayIn(loc *time.Location) time.Time {
	return dateOf(s.now().In(loc))
}

func (s *Service) today(t time.Time) time.Time {
	if t.IsZero() {
		return s.Today()
	}
	return dateOf(t)
}

// Create validates and stores a new expense.
func (s *Service) Create(ctx context.Context, in CreateInput) (Expense, error) {
	e := Expense{
		UserID:   in.UserID,
		Amount:   in.Amount,
		Category: in.Category,
		Comment:  in.Comment,
		SpentAt:  s.today(in.Today),
	}
	if in.SpentAt != nil {
		e.SpentAt = dateOf(*in.SpentAt)
	}
	if err := s.normalize(&e, s.today(in.Today)); err != nil {
		return Expense{}, err
	}
	return s.repo.Create(ctx, e)
}

// Get returns one expense visible in the scope.
func (s *Service) Get(ctx context.Context, sc Scope, id int64) (Expense, error) {
	return s.repo.Get(ctx, sc, id)
}

// Update applies a patch to an expense visible in the scope.
func (s *Service) Update(ctx context.Context, sc Scope, id int64, p Patch) (Expense, error) {
	if p.UserID != nil && sc.FamilyID() == 0 {
		// Only the head moves an expense between members of the family.
		return Expense{}, invalid("user_id", "cannot reassign an expense to another user")
	}
	e, err := s.repo.Get(ctx, sc, id)
	if err != nil {
		return Expense{}, err
	}
	if p.UserID != nil {
		e.UserID = *p.UserID
	}
	if p.Amount != nil {
		e.Amount = *p.Amount
	}
	if p.Category != nil {
		e.Category = *p.Category
	}
	if p.SpentAt != nil {
		e.SpentAt = dateOf(*p.SpentAt)
	}
	if p.Comment != nil {
		e.Comment = *p.Comment
	}
	if err := s.normalize(&e, s.today(p.Today)); err != nil {
		return Expense{}, err
	}
	return s.repo.Update(ctx, sc, e)
}

// Delete removes an expense visible in the scope.
func (s *Service) Delete(ctx context.Context, sc Scope, id int64) error {
	return s.repo.Delete(ctx, sc, id)
}

// List returns a page of expenses matching the filter.
func (s *Service) List(ctx context.Context, sc Scope, f Filter) (Page, error) {
	if err := normalizeFilter(&f); err != nil {
		return Page{}, err
	}
	return s.repo.List(ctx, sc, f)
}

// Summary returns spending grouped by category. Paging fields of f are ignored.
func (s *Service) Summary(ctx context.Context, sc Scope, f Filter) (Summary, error) {
	if err := normalizeFilter(&f); err != nil {
		return Summary{}, err
	}
	return s.repo.Summary(ctx, sc, f)
}

// normalize validates e and brings it to canonical form.
// today is the current date of whoever makes the change.
func (s *Service) normalize(e *Expense, today time.Time) error {
	if e.UserID <= 0 {
		return invalid("user_id", "must be a positive Telegram ID")
	}
	if !e.Amount.IsPositive() {
		return invalid("amount", "must be greater than zero")
	}
	if !e.Amount.Equal(e.Amount.Round(2)) {
		return invalid("amount", "must have at most 2 decimal places")
	}
	if e.Amount.GreaterThanOrEqual(maxAmount) {
		return invalid("amount", "is too large")
	}
	if !e.Category.Valid() {
		return invalid("category", "unknown category %q", e.Category)
	}
	if e.SpentAt.Before(minSpentAt) {
		return invalid("spent_at", "must not be earlier than %s", minSpentAt.Format(time.DateOnly))
	}
	if e.SpentAt.After(today) {
		return invalid("spent_at", "must not be in the future")
	}
	e.Comment = strings.TrimSpace(e.Comment)
	if utf8.RuneCountInString(e.Comment) > MaxCommentLength {
		return invalid("comment", "must be at most %d characters", MaxCommentLength)
	}
	return nil
}

func normalizeFilter(f *Filter) error {
	if f.Category != nil && !f.Category.Valid() {
		return invalid("category", "unknown category %q", *f.Category)
	}
	if f.From != nil {
		d := dateOf(*f.From)
		f.From = &d
	}
	if f.To != nil {
		d := dateOf(*f.To)
		f.To = &d
	}
	if f.From != nil && f.To != nil && f.From.After(*f.To) {
		return invalid("from", "must not be after 'to'")
	}
	f.Query = strings.TrimSpace(f.Query)
	switch {
	case f.Limit < 0:
		return invalid("limit", "must not be negative")
	case f.Limit == 0:
		f.Limit = DefaultLimit
	case f.Limit > MaxLimit:
		f.Limit = MaxLimit
	}
	if f.Offset < 0 {
		return invalid("offset", "must not be negative")
	}
	return nil
}

// dateOf drops the time of day, keeping the calendar date as seen in t's location.
func dateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
