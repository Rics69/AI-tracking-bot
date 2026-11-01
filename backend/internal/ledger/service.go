package ledger

import (
	"context"
	"errors"
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
	minDate = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
)

// Repository persists operations. Every method honours the scope:
// operations outside it behave as if they did not exist.
type Repository interface {
	Create(ctx context.Context, e Operation) (Operation, error)
	Get(ctx context.Context, s Scope, id int64) (Operation, error)
	Update(ctx context.Context, s Scope, e Operation) (Operation, error)
	Delete(ctx context.Context, s Scope, id int64) error
	List(ctx context.Context, s Scope, f Filter) (Page, error)
	Summary(ctx context.Context, s Scope, f Filter) (Summary, error)
	// Aggregate sums operations per bucket start and category; empty buckets are omitted.
	Aggregate(ctx context.Context, s Scope, f Filter, b Bucket) ([]BucketTotal, error)
}

// Rates converts foreign money at the rate of a date.
type Rates interface {
	On(ctx context.Context, code string, date time.Time) (decimal.Decimal, error)
}

// Accounts tells the currency of a user's account.
type Accounts interface {
	Currency(ctx context.Context, userID, id int64) (string, error) // ErrNoAccount
}

var (
	// ErrNoAccount: no such account among the user's own.
	ErrNoAccount = errors.New("no such account")
	// ErrNoRate: nothing is known about a currency yet.
	ErrNoRate = errors.New("no exchange rate yet")
)

// Service implements the ledger use cases on top of a Repository.
type Service struct {
	repo Repository
	// Rates and Accounts enable foreign currencies and accounts; without them
	// every operation is in rubles on the default account.
	Rates    Rates
	Accounts Accounts
	loc      *time.Location
	now      func() time.Time
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

// Create validates and stores a new operation.
func (s *Service) Create(ctx context.Context, in CreateInput) (Operation, error) {
	e := Operation{
		UserID:           in.UserID,
		Amount:           in.Amount,
		Category:         in.Category,
		Comment:          in.Comment,
		Date:             s.today(in.Today),
		RegularPaymentID: in.RegularPaymentID,
		Currency:         in.Currency,
		AccountID:        in.AccountID,
	}
	if in.Date != nil {
		e.Date = dateOf(*in.Date)
	}
	if err := s.price(ctx, &e, in.Amount); err != nil {
		return Operation{}, err
	}
	if err := s.normalize(&e, s.today(in.Today)); err != nil {
		return Operation{}, err
	}
	return s.repo.Create(ctx, e)
}

// price settles the currency and the account of an operation and turns what
// was paid into rubles. An account in a foreign currency takes only its own
// currency; a ruble account takes anything, at its ruble value.
func (s *Service) price(ctx context.Context, e *Operation, paid decimal.Decimal) error {
	if e.AccountID != nil {
		if s.Accounts == nil {
			return invalid("account_id", "accounts are off")
		}
		currency, err := s.Accounts.Currency(ctx, e.UserID, *e.AccountID)
		if errors.Is(err, ErrNoAccount) {
			return invalid("account_id", "no such account")
		}
		if err != nil {
			return err
		}
		if e.Currency == "" {
			e.Currency = currency
		}
		if currency != RUB && e.Currency != currency {
			return invalid("currency", "this account is in %s", currency)
		}
	}
	if e.Currency == "" {
		e.Currency = RUB
	}
	if !KnownCurrency(e.Currency) {
		return invalid("currency", "unknown currency %q", e.Currency)
	}
	if e.Currency == RUB {
		e.Amount, e.Original = paid, nil
		return nil
	}
	if s.Rates == nil {
		return invalid("currency", "only rubles here")
	}
	if !paid.IsPositive() || !paid.Equal(paid.Round(2)) || paid.GreaterThanOrEqual(maxAmount) {
		return invalid("amount", "must be positive, with at most 2 decimal places")
	}
	rate, err := s.Rates.On(ctx, e.Currency, e.Date)
	if errors.Is(err, ErrNoRate) {
		return invalid("currency", "no exchange rate yet")
	}
	if err != nil {
		return err
	}
	e.Amount, e.Original = paid.Mul(rate).Round(2), &paid
	return nil
}

// Get returns one operation visible in the scope.
func (s *Service) Get(ctx context.Context, sc Scope, id int64) (Operation, error) {
	return s.repo.Get(ctx, sc, id)
}

// Update applies a patch to an operation visible in the scope.
func (s *Service) Update(ctx context.Context, sc Scope, id int64, p Patch) (Operation, error) {
	if p.UserID != nil && sc.FamilyID() == 0 {
		// Only the head moves an operation between members of the family.
		return Operation{}, invalid("user_id", "cannot reassign an expense to another user")
	}
	e, err := s.repo.Get(ctx, sc, id)
	if err != nil {
		return Operation{}, err
	}
	if p.UserID != nil && *p.UserID != e.UserID {
		e.UserID = *p.UserID
		e.RegularPaymentID = nil // the new owner has regular payments of their own
		e.AccountID = nil        // and accounts of their own
	}
	paid := e.Paid()
	repriced := p.Amount != nil || p.Currency != nil || p.AccountID != nil || p.Date != nil && e.Original != nil
	if p.Amount != nil {
		paid = *p.Amount
	}
	if p.Currency != nil {
		e.Currency = *p.Currency
	}
	if p.AccountID != nil {
		e.AccountID = p.AccountID
	}
	if p.Category != nil {
		e.Category = *p.Category
		if e.Kind() != KindExpense {
			e.RegularPaymentID = nil // an income pays nothing
		}
	}
	if p.Date != nil {
		e.Date = dateOf(*p.Date)
	}
	if p.Comment != nil {
		e.Comment = *p.Comment
	}
	if repriced {
		if err := s.price(ctx, &e, paid); err != nil {
			return Operation{}, err
		}
	}
	if err := s.normalize(&e, s.today(p.Today)); err != nil {
		return Operation{}, err
	}
	return s.repo.Update(ctx, sc, e)
}

// Delete removes an operation visible in the scope.
func (s *Service) Delete(ctx context.Context, sc Scope, id int64) error {
	return s.repo.Delete(ctx, sc, id)
}

// List returns a page of operations matching the filter.
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
func (s *Service) normalize(e *Operation, today time.Time) error {
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
	if e.RegularPaymentID != nil && e.Kind() != KindExpense {
		return invalid("regular_payment_id", "only an expense can pay a regular payment")
	}
	if e.Date.Before(minDate) {
		return invalid("date", "must not be earlier than %s", minDate.Format(time.DateOnly))
	}
	if e.Date.After(today) {
		return invalid("date", "must not be in the future")
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
