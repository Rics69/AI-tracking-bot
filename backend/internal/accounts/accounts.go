// Package accounts knows where the money is: cards, cash, savings and foreign
// currency. A balance is the opening amount, plus incomes, minus expenses, with
// transfers moved between accounts; a transfer is neither an expense nor an
// income, so it touches no summary and no limit.
package accounts

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

// Kinds of accounts.
const (
	KindCard    = "card"
	KindCash    = "cash"
	KindSavings = "savings"
	KindForeign = "foreign"
	KindOther   = "other"
)

var kinds = []string{KindCard, KindCash, KindSavings, KindForeign, KindOther}

const (
	maxName     = 40
	maxAccounts = 20
	maxComment  = 200
)

// ErrNotFound: no such account among the user's own.
var ErrNotFound = errors.New("account not found")

// Account is a place where money lies.
type Account struct {
	ID         int64
	UserID     int64
	Name       string
	Kind       string
	Currency   string
	Opening    decimal.Decimal
	IsDefault  bool
	ArchivedAt *time.Time
	CreatedAt  time.Time
}

// Balance is an account with what is on it now.
type Balance struct {
	Account
	Amount decimal.Decimal // in the currency of the account
	Rubles decimal.Decimal // the same at the latest rate
}

// Transfer moves money between the user's accounts. The amounts differ when
// the currencies do: that is buying currency at one's own rate.
type Transfer struct {
	ID         int64
	UserID     int64
	FromID     int64
	ToID       int64
	AmountFrom decimal.Decimal
	AmountTo   decimal.Decimal
	Date       time.Time
	Comment    string
	CreatedAt  time.Time
}

// Store persists accounts. Every method works within one user's data.
type Store interface {
	EnsureDefault(ctx context.Context, userID int64, name string) error
	List(ctx context.Context, userID int64) ([]Account, error)
	Get(ctx context.Context, userID, id int64) (Account, error) // ErrNotFound
	Create(ctx context.Context, a Account) (Account, error)
	Update(ctx context.Context, a Account) (Account, error)
	// Operated sums incomes minus expenses per account, in its currency.
	Operated(ctx context.Context, userID int64) (map[int64]decimal.Decimal, error)
	AddTransfer(ctx context.Context, t Transfer) (Transfer, error)
	Transfers(ctx context.Context, userID int64) ([]Transfer, error) // newest first
	DeleteTransfer(ctx context.Context, userID, id int64) error
}

// Rates tells the latest exchange rates; a missing currency counts as nothing.
type Rates interface {
	Now(ctx context.Context, code string) (decimal.Decimal, error)
}

// Service manages accounts and computes balances.
type Service struct {
	store Store
	rates Rates
}

func NewService(store Store, rates Rates) *Service { return &Service{store: store, rates: rates} }

const defaultName = "Основной"

// Balances lists the user's accounts in use with what is on them, the default
// one created on the first call.
func (s *Service) Balances(ctx context.Context, userID int64) ([]Balance, decimal.Decimal, error) {
	list, err := s.accounts(ctx, userID)
	if err != nil {
		return nil, decimal.Zero, err
	}
	operated, err := s.store.Operated(ctx, userID)
	if err != nil {
		return nil, decimal.Zero, err
	}
	transfers, err := s.store.Transfers(ctx, userID)
	if err != nil {
		return nil, decimal.Zero, err
	}
	out := make([]Balance, 0, len(list))
	total := decimal.Zero
	for _, a := range list {
		if a.ArchivedAt != nil {
			continue
		}
		amount := a.Opening.Add(operated[a.ID])
		for _, t := range transfers {
			if t.FromID == a.ID {
				amount = amount.Sub(t.AmountFrom)
			}
			if t.ToID == a.ID {
				amount = amount.Add(t.AmountTo)
			}
		}
		rubles := amount
		if a.Currency != ledger.RUB {
			rate, err := s.rates.Now(ctx, a.Currency)
			if err != nil && !errors.Is(err, ledger.ErrNoRate) {
				return nil, decimal.Zero, err
			}
			rubles = amount.Mul(rate).Round(2)
		}
		total = total.Add(rubles)
		out = append(out, Balance{Account: a, Amount: amount, Rubles: rubles})
	}
	return out, total, nil
}

func (s *Service) accounts(ctx context.Context, userID int64) ([]Account, error) {
	if err := s.store.EnsureDefault(ctx, userID, defaultName); err != nil {
		return nil, err
	}
	return s.store.List(ctx, userID)
}

// Currency is the currency of an account; ledger.Service asks it.
func (s *Service) Currency(ctx context.Context, userID, id int64) (string, error) {
	a, err := s.store.Get(ctx, userID, id)
	if errors.Is(err, ErrNotFound) {
		return "", ledger.ErrNoAccount
	}
	return a.Currency, err
}

// ByName finds an account in use by a part of its name: "нал" is «Наличные».
func (s *Service) ByName(ctx context.Context, userID int64, name string) (Account, error) {
	list, err := s.accounts(ctx, userID)
	if err != nil {
		return Account{}, err
	}
	want := strings.ToLower(strings.TrimSpace(name))
	var found []Account
	for _, a := range list {
		if a.ArchivedAt != nil {
			continue
		}
		have := strings.ToLower(a.Name)
		if have == want {
			return a, nil
		}
		if strings.Contains(have, want) || strings.Contains(want, have) || sameStart(have, want) {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		return Account{}, ErrNotFound
	}
	return found[0], nil
}

// sameStart compares words by their beginning: "наличкой" and «Наличные».
func sameStart(a, b string) bool {
	x, y := []rune(a), []rune(b)
	const stem = 4
	if len(x) < stem || len(y) < stem {
		return false
	}
	return string(x[:stem]) == string(y[:stem])
}

// Names lists the accounts in use, for the agent.
func (s *Service) Names(ctx context.Context, userID int64) ([]string, error) {
	list, err := s.accounts(ctx, userID)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, a := range list {
		if a.ArchivedAt == nil {
			names = append(names, a.Name)
		}
	}
	return names, nil
}

func invalid(field, message string) error {
	return &ledger.ValidationError{Field: field, Message: message}
}

func checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxName {
		return "", invalid("name", "from 1 to 40 characters")
	}
	return name, nil
}

// Create opens an account.
func (s *Service) Create(ctx context.Context, a Account) (Account, error) {
	var err error
	if a.Name, err = checkName(a.Name); err != nil {
		return Account{}, err
	}
	if a.Kind == "" {
		a.Kind = KindCard
	}
	if !slices.Contains(kinds, a.Kind) {
		return Account{}, invalid("kind", "unknown kind of account")
	}
	if a.Currency == "" {
		a.Currency = ledger.RUB
	}
	if !ledger.KnownCurrency(a.Currency) {
		return Account{}, invalid("currency", "unknown currency")
	}
	list, err := s.accounts(ctx, a.UserID)
	if err != nil {
		return Account{}, err
	}
	if len(list) >= maxAccounts {
		return Account{}, invalid("accounts", "at most 20 accounts")
	}
	return s.store.Create(ctx, a)
}

// Patch holds what can change in an account; nil fields stay. Balance sets what
// is on the account now by changing the opening amount: correcting a balance
// must not look like spending.
type Patch struct {
	Name     *string
	Kind     *string
	Balance  *decimal.Decimal
	Archived *bool
}

// Update changes an account. The default one cannot be archived: operations
// without an account land there.
func (s *Service) Update(ctx context.Context, userID, id int64, p Patch, now time.Time) (Account, error) {
	a, err := s.store.Get(ctx, userID, id)
	if err != nil {
		return Account{}, err
	}
	if p.Name != nil {
		if a.Name, err = checkName(*p.Name); err != nil {
			return Account{}, err
		}
	}
	if p.Kind != nil {
		if !slices.Contains(kinds, *p.Kind) {
			return Account{}, invalid("kind", "unknown kind of account")
		}
		a.Kind = *p.Kind
	}
	if p.Archived != nil {
		if *p.Archived && a.IsDefault {
			return Account{}, invalid("archived", "the default account stays")
		}
		a.ArchivedAt = nil
		if *p.Archived {
			a.ArchivedAt = &now
		}
	}
	if p.Balance != nil {
		list, _, err := s.Balances(ctx, userID)
		if err != nil {
			return Account{}, err
		}
		for _, b := range list {
			if b.ID == id {
				a.Opening = a.Opening.Add(p.Balance.Sub(b.Amount))
			}
		}
	}
	return s.store.Update(ctx, a)
}

// TransferInput is a transfer to record. AmountTo may be left zero between
// accounts of one currency.
type TransferInput struct {
	UserID     int64
	FromID     int64
	ToID       int64
	AmountFrom decimal.Decimal
	AmountTo   decimal.Decimal
	Date       time.Time
	Comment    string
	Today      time.Time
}

// Transfer records moving money between the user's accounts.
func (s *Service) Transfer(ctx context.Context, in TransferInput) (Transfer, error) {
	if in.FromID == in.ToID {
		return Transfer{}, invalid("to_account_id", "pick two different accounts")
	}
	from, err := s.store.Get(ctx, in.UserID, in.FromID)
	if err != nil {
		return Transfer{}, err
	}
	to, err := s.store.Get(ctx, in.UserID, in.ToID)
	if err != nil {
		return Transfer{}, err
	}
	if !in.AmountFrom.IsPositive() {
		return Transfer{}, invalid("amount_from", "must be greater than zero")
	}
	if in.AmountTo.IsZero() {
		if from.Currency != to.Currency {
			return Transfer{}, invalid("amount_to", "how much arrived: the currencies differ")
		}
		in.AmountTo = in.AmountFrom
	}
	if in.AmountTo.IsNegative() {
		return Transfer{}, invalid("amount_to", "must be greater than zero")
	}
	if from.Currency == to.Currency && !in.AmountTo.Equal(in.AmountFrom) {
		return Transfer{}, invalid("amount_to", "the same currency: the amounts must match")
	}
	if in.Date.IsZero() {
		in.Date = in.Today
	}
	if in.Date.After(in.Today) {
		return Transfer{}, invalid("date", "must not be in the future")
	}
	comment := strings.TrimSpace(in.Comment)
	if utf8.RuneCountInString(comment) > maxComment {
		return Transfer{}, invalid("comment", "at most 200 characters")
	}
	return s.store.AddTransfer(ctx, Transfer{UserID: in.UserID, FromID: in.FromID, ToID: in.ToID,
		AmountFrom: in.AmountFrom.Round(2), AmountTo: in.AmountTo.Round(2), Date: in.Date, Comment: comment})
}

// Transfers lists the user's transfers, newest first.
func (s *Service) Transfers(ctx context.Context, userID int64) ([]Transfer, error) {
	return s.store.Transfers(ctx, userID)
}

// DeleteTransfer takes a transfer back.
func (s *Service) DeleteTransfer(ctx context.Context, userID, id int64) error {
	return s.store.DeleteTransfer(ctx, userID, id)
}
