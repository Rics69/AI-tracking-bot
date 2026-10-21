// Package ledger is the money domain: operations (expenses and incomes),
// validation rules and the service used by both the REST API and the Telegram bot.
package ledger

import (
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// Kind tells money spent from money received.
type Kind string

const (
	KindExpense Kind = "expense"
	KindIncome  Kind = "income"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return k == KindExpense || k == KindIncome }

// Category is a fixed category of an operation; it also decides the kind:
// "salary" is always an income, "food" always an expense. Codes are stored
// in the database; human-readable labels are only for presentation.
type Category string

const (
	CategoryFood          Category = "food"
	CategoryTransport     Category = "transport"
	CategoryHousing       Category = "housing"
	CategoryEntertainment Category = "entertainment"
	CategoryHealth        Category = "health"
	CategoryShopping      Category = "shopping"
	CategoryOther         Category = "other"

	CategorySalary      Category = "salary"
	CategoryFreelance   Category = "freelance"
	CategoryGift        Category = "gift"
	CategoryInterest    Category = "interest"
	CategoryRefund      Category = "refund"
	CategoryOtherIncome Category = "other_income"
)

// ExpenseCategories and IncomeCategories list the categories in display order.
var (
	ExpenseCategories = []Category{
		CategoryFood, CategoryTransport, CategoryHousing, CategoryEntertainment,
		CategoryHealth, CategoryShopping, CategoryOther,
	}
	IncomeCategories = []Category{
		CategorySalary, CategoryFreelance, CategoryGift, CategoryInterest, CategoryRefund, CategoryOtherIncome,
	}
)

var categoryLabels = map[Category]string{
	CategoryFood:          "еда",
	CategoryTransport:     "транспорт",
	CategoryHousing:       "жильё",
	CategoryEntertainment: "развлечения",
	CategoryHealth:        "здоровье",
	CategoryShopping:      "покупки",
	CategoryOther:         "другое",

	CategorySalary:      "зарплата",
	CategoryFreelance:   "подработка",
	CategoryGift:        "подарок",
	CategoryInterest:    "проценты и кэшбэк",
	CategoryRefund:      "возврат",
	CategoryOtherIncome: "другие доходы",
}

// Valid reports whether c is one of the known categories.
func (c Category) Valid() bool {
	_, ok := categoryLabels[c]
	return ok
}

// Label returns the Russian name of the category.
func (c Category) Label() string { return categoryLabels[c] }

// Kind returns the kind of operations in the category.
func (c Category) Kind() Kind {
	switch c {
	case CategorySalary, CategoryFreelance, CategoryGift, CategoryInterest, CategoryRefund, CategoryOtherIncome:
		return KindIncome
	default:
		return KindExpense
	}
}

// Operation is a single record of money spent or received by a user.
type Operation struct {
	ID        int64
	UserID    int64 // Telegram ID of the owner
	Amount    decimal.Decimal
	Category  Category
	Date      time.Time // calendar date, midnight UTC
	Comment   string    // empty means no comment
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Kind is the kind of the operation, decided by its category.
func (o Operation) Kind() Kind { return o.Category.Kind() }

// CreateInput holds the fields of a new operation; the category decides
// whether it is an expense or an income.
type CreateInput struct {
	UserID   int64
	Amount   decimal.Decimal
	Category Category
	Date     *time.Time // nil means Today
	Comment  string
	// Today is the current date of whoever records the operation, in their
	// timezone: later dates are rejected. Zero means today in the service timezone.
	Today time.Time
}

// Patch holds the fields to change; nil fields are left as is.
// An empty Comment clears the comment.
type Patch struct {
	UserID   *int64
	Amount   *decimal.Decimal
	Category *Category
	Date     *time.Time
	Comment  *string
	// Today is the current date of whoever edits, as in CreateInput.
	Today time.Time
}

// Sort is the order of operations in a list.
type Sort int

const (
	// SortByDate orders by the operation date, newest first (default).
	SortByDate Sort = iota
	// SortByCreatedAt orders by creation time, newest first ("the last one I added").
	SortByCreatedAt
	// SortByAmount orders by amount, largest first ("the biggest expenses").
	SortByAmount
)

// Filter selects operations for listing and summaries. Zero fields mean "any".
type Filter struct {
	UserID   *int64
	Kind     *Kind
	Category *Category
	From, To *time.Time // inclusive calendar dates
	Query    string     // case-insensitive substring of the comment
	Sort     Sort
	Limit    int
	Offset   int
}

// Page is one page of a list together with totals over the whole selection.
type Page struct {
	Items      []Operation
	Total      int             // number of matching operations
	ExpenseSum decimal.Decimal // sum of matching expenses
	IncomeSum  decimal.Decimal // sum of matching incomes
}

// CategoryTotal is the money in one category.
type CategoryTotal struct {
	Category Category
	Sum      decimal.Decimal
	Count    int
}

// Totals is the sum and number of operations of one kind.
type Totals struct {
	Sum   decimal.Decimal
	Count int
}

// Summary is money grouped by category, with totals per kind.
type Summary struct {
	Items   []CategoryTotal // both kinds, sorted by Sum, largest first
	Expense Totals
	Income  Totals
}

// Balance is income minus expense: what is left of the money received.
func (s Summary) Balance() decimal.Decimal { return s.Income.Sum.Sub(s.Expense.Sum) }

// Of returns the categories of one kind, largest first.
func (s Summary) Of(k Kind) []CategoryTotal {
	out := []CategoryTotal{}
	for _, t := range s.Items {
		if t.Category.Kind() == k {
			out = append(out, t)
		}
	}
	return out
}

// NewSummary builds a summary from category totals sorted largest first.
func NewSummary(items []CategoryTotal) Summary {
	s := Summary{Items: items, Expense: Totals{Sum: decimal.Zero}, Income: Totals{Sum: decimal.Zero}}
	for _, t := range items {
		k := &s.Expense
		if t.Category.Kind() == KindIncome {
			k = &s.Income
		}
		k.Sum = k.Sum.Add(t.Sum)
		k.Count += t.Count
	}
	return s
}

// Scope limits which operations a call may see or modify.
//
// The zero value matches no one, so a caller that forgets to set a scope
// gets "not found" instead of someone else's data (fail closed).
//
// There is deliberately no scope over all users: the database holds many
// families, and none may see another's expenses.
type Scope struct {
	userID   int64
	familyID int64
}

// OwnedBy restricts calls to the operations of one Telegram user.
func OwnedBy(userID int64) Scope { return Scope{userID: userID} }

// InFamily is the scope of a family head: the operations of everyone who is a
// member of the family now. Whoever leaves takes their operations along.
func InFamily(familyID int64) Scope { return Scope{familyID: familyID} }

// UserID returns the owner of an OwnedBy scope, 0 otherwise.
func (s Scope) UserID() int64 { return s.userID }

// FamilyID returns the family of an InFamily scope, 0 otherwise.
func (s Scope) FamilyID() int64 { return s.familyID }

// Errors returned by the service and repositories.
var (
	ErrNotFound    = errors.New("operation not found")
	ErrUnknownUser = errors.New("user not found")
)

// ValidationError describes an invalid input field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Message) }

func invalid(field, format string, args ...any) error {
	return &ValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}
