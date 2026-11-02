// Package debts keeps what the user owes, the plan to pay it off and the net
// worth. It counts and explains; what to do with money stays the user's call.
package debts

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/accounts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

// Kinds of debts.
const (
	KindCard        = "card"
	KindLoan        = "loan"
	KindInstallment = "installment"
	KindMicroloan   = "microloan"
	KindFriend      = "friend"
)

var kinds = []string{KindCard, KindLoan, KindInstallment, KindMicroloan, KindFriend}

const (
	maxName  = 60
	maxDebts = 30
	maxRate  = 1000
)

// ErrNotFound: no such debt among the user's own.
var ErrNotFound = errors.New("debt not found")

// Debt is money owed.
type Debt struct {
	ID         int64
	UserID     int64
	Name       string
	Kind       string
	Principal  decimal.Decimal // the balance when it was entered
	Rate       decimal.Decimal // percent a year
	MinPayment decimal.Decimal
	DueDay     *int // of the month; nil: no fixed day
	StartedOn  time.Time
	ClosedAt   *time.Time
	Balance    decimal.Decimal // what is left; filled by the repository
}

// Payment is money paid on a debt.
type Payment struct {
	ID          int64
	DebtID      int64
	UserID      int64
	Amount      decimal.Decimal
	Date        time.Time
	OperationID *int64
}

// Snapshot is the net worth at the end of a month, or as of now for the current one.
type Snapshot struct {
	Month  time.Time
	Assets decimal.Decimal
	Debts  decimal.Decimal
}

// Net is what remains when everything is paid.
func (s Snapshot) Net() decimal.Decimal { return s.Assets.Sub(s.Debts) }

// Store persists debts within one user's data, except where said.
type Store interface {
	List(ctx context.Context, userID int64) ([]Debt, error)
	Get(ctx context.Context, userID, id int64) (Debt, error) // ErrNotFound
	Create(ctx context.Context, d Debt) (Debt, error)
	Update(ctx context.Context, d Debt) (Debt, error)
	Delete(ctx context.Context, userID, id int64) error
	AddPayment(ctx context.Context, p Payment) (Payment, error)
	Payments(ctx context.Context, userID, debtID int64) ([]Payment, error) // newest first
	DeletePayment(ctx context.Context, userID, id int64) (Payment, error)
	// Open lists the unpaid debts of every user.
	Open(ctx context.Context) ([]Debt, error)
	// ClosedUsers lists who has closed at least one debt.
	ClosedUsers(ctx context.Context) ([]int64, error)
	SaveSnapshot(ctx context.Context, userID int64, s Snapshot) error
	Snapshots(ctx context.Context, userID int64, from time.Time) ([]Snapshot, error) // oldest first
}

// Ledger records payments as expenses: the money really leaves.
type Ledger interface {
	Create(ctx context.Context, in ledger.CreateInput) (ledger.Operation, error)
	Delete(ctx context.Context, s ledger.Scope, id int64) error
}

// Assets tells what the user has; *accounts.Service implements it.
type Assets interface {
	Balances(ctx context.Context, userID int64) ([]accounts.Balance, decimal.Decimal, error)
}

// Service manages debts.
type Service struct {
	store  Store
	ledger Ledger
	assets Assets
}

func NewService(store Store, l Ledger, a Assets) *Service {
	return &Service{store: store, ledger: l, assets: a}
}

func invalid(field, format string, args ...any) error {
	return &ledger.ValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}

func (d *Debt) normalize() error {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" || utf8.RuneCountInString(d.Name) > maxName {
		return invalid("name", "from 1 to %d characters", maxName)
	}
	if d.Kind == "" {
		d.Kind = KindLoan
	}
	if !slices.Contains(kinds, d.Kind) {
		return invalid("kind", "unknown kind of debt")
	}
	if d.Rate.IsNegative() || d.Rate.GreaterThan(decimal.NewFromInt(maxRate)) {
		return invalid("rate", "from 0 to %d percent a year", maxRate)
	}
	if d.MinPayment.IsNegative() {
		return invalid("min_payment", "must not be negative")
	}
	if d.DueDay != nil && (*d.DueDay < 1 || *d.DueDay > 31) {
		return invalid("due_day", "a day of the month is from 1 to 31")
	}
	return nil
}

// List is the user's open debts, largest rate first, and what they add up to.
func (s *Service) List(ctx context.Context, userID int64) ([]Debt, decimal.Decimal, error) {
	all, err := s.store.List(ctx, userID)
	if err != nil {
		return nil, decimal.Zero, err
	}
	open := []Debt{}
	total := decimal.Zero
	for _, d := range all {
		if d.Balance.IsPositive() {
			open = append(open, d)
			total = total.Add(d.Balance)
		}
	}
	slices.SortStableFunc(open, func(a, b Debt) int { return b.Rate.Cmp(a.Rate) })
	return open, total, nil
}

// Create adds a debt; Principal is what is owed now.
func (s *Service) Create(ctx context.Context, d Debt, today time.Time) (Debt, error) {
	if err := d.normalize(); err != nil {
		return Debt{}, err
	}
	if !d.Principal.IsPositive() {
		return Debt{}, invalid("balance", "must be greater than zero")
	}
	all, err := s.store.List(ctx, d.UserID)
	if err != nil {
		return Debt{}, err
	}
	if len(all) >= maxDebts {
		return Debt{}, invalid("debts", "at most %d debts", maxDebts)
	}
	d.StartedOn = today
	return s.store.Create(ctx, d)
}

// Patch holds what can change; nil fields stay. Balance corrects what is owed
// now, which is how interest the bank has added gets in.
type Patch struct {
	Name       *string
	Kind       *string
	Rate       *decimal.Decimal
	MinPayment *decimal.Decimal
	DueDay     *int
	Balance    *decimal.Decimal
}

// Update changes a debt.
func (s *Service) Update(ctx context.Context, userID, id int64, p Patch, now time.Time) (Debt, error) {
	d, err := s.store.Get(ctx, userID, id)
	if err != nil {
		return Debt{}, err
	}
	if p.Name != nil {
		d.Name = *p.Name
	}
	if p.Kind != nil {
		d.Kind = *p.Kind
	}
	if p.Rate != nil {
		d.Rate = *p.Rate
	}
	if p.MinPayment != nil {
		d.MinPayment = *p.MinPayment
	}
	if p.DueDay != nil {
		d.DueDay = p.DueDay
	}
	if err := d.normalize(); err != nil {
		return Debt{}, err
	}
	if p.Balance != nil {
		if p.Balance.IsNegative() {
			return Debt{}, invalid("balance", "must not be negative")
		}
		d.Principal = d.Principal.Add(p.Balance.Sub(d.Balance))
		d.Balance = *p.Balance
		d.ClosedAt = closing(d, now)
	}
	return s.store.Update(ctx, d)
}

func closing(d Debt, now time.Time) *time.Time {
	if d.Balance.IsPositive() {
		return nil
	}
	if d.ClosedAt != nil {
		return d.ClosedAt
	}
	return &now
}

// Delete removes a debt with its payments; the expenses stay, the money was spent.
func (s *Service) Delete(ctx context.Context, userID, id int64) error {
	return s.store.Delete(ctx, userID, id)
}

// PaymentInput is a payment to record.
type PaymentInput struct {
	UserID           int64
	DebtID           int64
	Amount           decimal.Decimal
	Date             *time.Time // nil: today
	AccountID        *int64
	RegularPaymentID *int64 // when the payment is also a regular one
	Today            time.Time
	Now              time.Time
}

// Pay records a payment: the debt goes down, and the money leaves as an
// expense, so accounts and the daily limit see it. Paying more than is left
// is a mistake to ask about, not a reason to go negative.
func (s *Service) Pay(ctx context.Context, in PaymentInput) (Debt, error) {
	d, err := s.store.Get(ctx, in.UserID, in.DebtID)
	if err != nil {
		return Debt{}, err
	}
	if !in.Amount.IsPositive() {
		return Debt{}, invalid("amount", "must be greater than zero")
	}
	if in.Amount.GreaterThan(d.Balance) {
		return Debt{}, invalid("amount", "only %s is left on this debt", d.Balance.StringFixed(2))
	}
	op, err := s.ledger.Create(ctx, ledger.CreateInput{
		UserID: in.UserID, Amount: in.Amount, Category: ledger.CategoryOther, Date: in.Date,
		Comment: "Платёж по долгу: " + d.Name, AccountID: in.AccountID, RegularPaymentID: in.RegularPaymentID, Today: in.Today,
	})
	if err != nil {
		return Debt{}, err
	}
	if _, err := s.store.AddPayment(ctx, Payment{DebtID: d.ID, UserID: in.UserID, Amount: in.Amount, Date: op.Date, OperationID: &op.ID}); err != nil {
		return Debt{}, err
	}
	d.Balance = d.Balance.Sub(in.Amount)
	d.ClosedAt = closing(d, in.Now)
	return s.store.Update(ctx, d)
}

// Payments lists the payments of a debt, newest first.
func (s *Service) Payments(ctx context.Context, userID, debtID int64) ([]Payment, error) {
	if _, err := s.store.Get(ctx, userID, debtID); err != nil {
		return nil, err
	}
	return s.store.Payments(ctx, userID, debtID)
}

// CancelPayment takes a payment back with its expense.
func (s *Service) CancelPayment(ctx context.Context, userID, id int64) error {
	p, err := s.store.DeletePayment(ctx, userID, id)
	if err != nil {
		return err
	}
	if p.OperationID != nil {
		if err := s.ledger.Delete(ctx, ledger.OwnedBy(userID), *p.OperationID); err != nil && !errors.Is(err, ledger.ErrNotFound) {
			return err
		}
	}
	d, err := s.store.Get(ctx, userID, p.DebtID)
	if err != nil {
		return err
	}
	d.ClosedAt = nil
	_, err = s.store.Update(ctx, d)
	return err
}

// ByName finds an open debt by a part of its name: "кредитка" is «Кредитка Тинькофф».
func (s *Service) ByName(ctx context.Context, userID int64, name string) (Debt, error) {
	open, _, err := s.List(ctx, userID)
	if err != nil {
		return Debt{}, err
	}
	want := strings.ToLower(strings.TrimSpace(name))
	var found []Debt
	for _, d := range open {
		have := strings.ToLower(d.Name)
		if have == want {
			return d, nil
		}
		if strings.Contains(have, want) || strings.Contains(want, have) {
			found = append(found, d)
		}
	}
	if len(found) != 1 {
		return Debt{}, ErrNotFound
	}
	return found[0], nil
}

// Plan works out both ways of paying off the open debts with extra a month on
// top of the minimums, starting next month.
func (s *Service) Plan(ctx context.Context, userID int64, extra decimal.Decimal, today time.Time) (Schedule, Schedule, error) {
	open, _, err := s.List(ctx, userID)
	if err != nil {
		return Schedule{}, Schedule{}, err
	}
	if extra.IsNegative() {
		return Schedule{}, Schedule{}, invalid("extra", "must not be negative")
	}
	return Schedules(open, extra, today)
}

// NetWorth is what the user has minus what they owe, now.
func (s *Service) NetWorth(ctx context.Context, userID int64, today time.Time) (Snapshot, error) {
	_, assets, err := s.assets.Balances(ctx, userID)
	if err != nil {
		return Snapshot{}, err
	}
	_, debts, err := s.List(ctx, userID)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Month: monthOf(today), Assets: assets, Debts: debts}, nil
}

func monthOf(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// historyMonths is how far back the chart goes.
const historyMonths = 24

// History is the net worth by month, oldest first.
func (s *Service) History(ctx context.Context, userID int64, today time.Time) ([]Snapshot, error) {
	return s.store.Snapshots(ctx, userID, monthOf(today).AddDate(0, 1-historyMonths, 0))
}

// Snap writes this month's snapshot; the last one of a month stays as its result.
func (s *Service) Snap(ctx context.Context, userID int64, today time.Time) error {
	now, err := s.NetWorth(ctx, userID, today)
	if err != nil || now.Assets.IsZero() && now.Debts.IsZero() {
		return err
	}
	return s.store.SaveSnapshot(ctx, userID, now)
}

// ClosedUsers lists who has closed at least one debt.
func (s *Service) ClosedUsers(ctx context.Context) ([]int64, error) { return s.store.ClosedUsers(ctx) }

// Users lists who has an open debt: they get a line in the morning message.
func (s *Service) Users(ctx context.Context) ([]int64, error) {
	open, err := s.store.Open(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(open))
	for i, d := range open {
		ids[i] = d.UserID
	}
	return ids, nil
}

// DueOn lists the user's open debts with a payment on a date.
func (s *Service) DueOn(ctx context.Context, userID int64, date time.Time) ([]Debt, error) {
	open, _, err := s.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	last := date.AddDate(0, 1, -date.Day()).Day()
	var out []Debt
	for _, d := range open {
		if d.DueDay != nil && min(*d.DueDay, last) == date.Day() {
			out = append(out, d)
		}
	}
	return out, nil
}
