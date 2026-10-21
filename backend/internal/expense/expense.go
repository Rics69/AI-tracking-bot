// Package expense contains the expense domain: entities, validation rules
// and the service used by both the REST API and the Telegram bot.
package expense

import (
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// Category is a fixed expense category. Codes are stored in the database;
// human-readable labels are only for presentation.
type Category string

const (
	CategoryFood          Category = "food"
	CategoryTransport     Category = "transport"
	CategoryHousing       Category = "housing"
	CategoryEntertainment Category = "entertainment"
	CategoryHealth        Category = "health"
	CategoryShopping      Category = "shopping"
	CategoryOther         Category = "other"
)

// Categories lists all categories in display order.
var Categories = []Category{
	CategoryFood, CategoryTransport, CategoryHousing, CategoryEntertainment,
	CategoryHealth, CategoryShopping, CategoryOther,
}

var categoryLabels = map[Category]string{
	CategoryFood:          "еда",
	CategoryTransport:     "транспорт",
	CategoryHousing:       "жильё",
	CategoryEntertainment: "развлечения",
	CategoryHealth:        "здоровье",
	CategoryShopping:      "покупки",
	CategoryOther:         "другое",
}

// Valid reports whether c is one of the known categories.
func (c Category) Valid() bool {
	_, ok := categoryLabels[c]
	return ok
}

// Label returns the Russian name of the category.
func (c Category) Label() string { return categoryLabels[c] }

// Expense is a single spending record of a family member.
type Expense struct {
	ID        int64
	UserID    int64 // Telegram ID of the owner
	Amount    decimal.Decimal
	Category  Category
	SpentAt   time.Time // calendar date, midnight UTC
	Comment   string    // empty means no comment
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateInput holds the fields of a new expense.
type CreateInput struct {
	UserID   int64
	Amount   decimal.Decimal
	Category Category
	SpentAt  *time.Time // nil means Today
	Comment  string
	// Today is the current date of whoever records the expense, in their
	// timezone: later dates are rejected. Zero means today in the service timezone.
	Today time.Time
}

// Patch holds the fields to change; nil fields are left as is.
// An empty Comment clears the comment.
type Patch struct {
	UserID   *int64
	Amount   *decimal.Decimal
	Category *Category
	SpentAt  *time.Time
	Comment  *string
	// Today is the current date of whoever edits, as in CreateInput.
	Today time.Time
}

// Sort is the order of expenses in a list.
type Sort int

const (
	// SortBySpentAt orders by spending date, newest first (default).
	SortBySpentAt Sort = iota
	// SortByCreatedAt orders by creation time, newest first ("the last one I added").
	SortByCreatedAt
	// SortByAmount orders by amount, largest first ("the biggest expenses").
	SortByAmount
)

// Filter selects expenses for listing and summaries. Zero fields mean "any".
type Filter struct {
	UserID   *int64
	Category *Category
	From, To *time.Time // inclusive calendar dates
	Query    string     // case-insensitive substring of the comment
	Sort     Sort
	Limit    int
	Offset   int
}

// Page is one page of a list together with totals over the whole selection.
type Page struct {
	Items []Expense
	Total int             // number of matching expenses
	Sum   decimal.Decimal // sum of matching expenses
}

// CategoryTotal is the spending in one category.
type CategoryTotal struct {
	Category Category
	Sum      decimal.Decimal
	Count    int
}

// Summary is spending grouped by category.
type Summary struct {
	Items []CategoryTotal // sorted by Sum, largest first
	Sum   decimal.Decimal
	Count int
}

// Scope limits which expenses an operation may see or modify.
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

// OwnedBy restricts operations to the expenses of one Telegram user.
func OwnedBy(userID int64) Scope { return Scope{userID: userID} }

// InFamily is the scope of a family head: the expenses of everyone who is a
// member of the family now. Whoever leaves takes their expenses along.
func InFamily(familyID int64) Scope { return Scope{familyID: familyID} }

// UserID returns the owner of an OwnedBy scope, 0 otherwise.
func (s Scope) UserID() int64 { return s.userID }

// FamilyID returns the family of an InFamily scope, 0 otherwise.
func (s Scope) FamilyID() int64 { return s.familyID }

// Errors returned by the service and repositories.
var (
	ErrNotFound    = errors.New("expense not found")
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
