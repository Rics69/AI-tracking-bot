package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

// Case is one eval scenario: a dialog with the agent and what must be true after it.
type Case struct {
	Name string          `yaml:"name"`
	Tags []string        `yaml:"tags"`
	Seed []OperationSpec `yaml:"seed"` // the user's operations before the dialog, in creation order
	// Plan, Goals and Payments are the user's limit settings before the dialog.
	Plan     *PlanSpec     `yaml:"plan"`
	Goals    []GoalSpec    `yaml:"goals"`
	Payments []PaymentSpec `yaml:"payments"`
	Turns    []string      `yaml:"turns"` // user messages, sent one by one in one conversation
	Expect   Expect        `yaml:"expect"`
}

// Expect lists the checks of a case. Empty fields are not checked.
type Expect struct {
	// Operations is the exact final state of the user's expenses and incomes (order does not matter).
	Operations *[]OperationSpec `yaml:"operations"`
	// Unchanged means the user's operations must equal the seed.
	Unchanged bool `yaml:"unchanged"`
	// Tools must each be called at least once.
	Tools []string `yaml:"tools"`
	// NoTools means the agent must answer without calling any tool.
	NoTools bool `yaml:"no_tools"`
	// ReplyContains / ReplyNotContains are checked against the last reply,
	// case-insensitively and ignoring whitespace ("1200" matches "1 200 ₽").
	// "сумм|стоимост" in ReplyContains accepts either word.
	ReplyContains    []string `yaml:"reply_contains"`
	ReplyNotContains []string `yaml:"reply_not_contains"`
	// ReplyHasLimit: the last reply quotes today's limit, computed after the dialog.
	ReplyHasLimit bool `yaml:"reply_has_limit"`
	// Plan, Goals and Payments are the exact final limit settings; nil is not checked.
	Plan     *PlanSpec      `yaml:"plan"`
	Goals    *[]GoalSpec    `yaml:"goals"`
	Payments *[]PaymentSpec `yaml:"payments"`
}

// PlanSpec is the pay plan: paydays and, optionally, the expected income.
type PlanSpec struct {
	Paydays        []int  `yaml:"paydays"`
	ExpectedIncome string `yaml:"expected_income"` // empty: none (expectations: not checked)
}

// GoalSpec is a goal. In expectations Title matches by substring, case-insensitively.
type GoalSpec struct {
	Title  string `yaml:"title"`
	Target string `yaml:"target"`
	Saved  string `yaml:"saved"` // empty: zero
}

// PaymentSpec is a regular payment. In expectations Title matches by
// substring, case-insensitively, and an empty category is not checked.
type PaymentSpec struct {
	Title    string `yaml:"title"`
	Amount   string `yaml:"amount"`
	Day      int    `yaml:"day"`
	Category string `yaml:"category"`
}

// OperationSpec describes an expense or an income in a case file.
type OperationSpec struct {
	Amount   string `yaml:"amount"`
	Category string `yaml:"category"`
	// Kind (expense or income) lets an expectation accept any category of that
	// kind when the right category is a matter of taste; seeds need a category.
	Kind string `yaml:"kind"`
	// Date is relative to the day the eval runs: today, yesterday, -N (days ago),
	// a weekday name (the most recent one, today included) or day:N (N-th of this month).
	Date string `yaml:"date"`
	// Comment must be contained in the actual comment (case-insensitive); empty matches any.
	Comment string `yaml:"comment"`
	// Payment is the title of the regular payment the expense pays: seeds are
	// linked to it, expectations require the link; empty means no link.
	Payment string `yaml:"payment"`
}

func loadCases(path string) ([]Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []Case
	if err := yaml.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for i, c := range cases {
		if c.Name == "" || len(c.Turns) == 0 {
			return nil, fmt.Errorf("case #%d: name and turns are required", i+1)
		}
	}
	return cases, nil
}

// resolved is an OperationSpec with concrete values.
type resolved struct {
	Amount   decimal.Decimal
	Category ledger.Category // empty: any category of Kind
	Kind     ledger.Kind
	Date     time.Time
	Comment  string
	Payment  string
}

func (s OperationSpec) resolve(today time.Time) (resolved, error) {
	amount, err := decimal.NewFromString(s.Amount)
	if err != nil {
		return resolved{}, fmt.Errorf("amount %q: %w", s.Amount, err)
	}
	c, k := ledger.Category(s.Category), ledger.Kind(s.Kind)
	switch {
	case c == "" && !k.Valid():
		return resolved{}, fmt.Errorf("a category or a kind is required, got kind %q", s.Kind)
	case c != "" && !c.Valid():
		return resolved{}, fmt.Errorf("unknown category %q", s.Category)
	case c != "" && k != "" && c.Kind() != k:
		return resolved{}, fmt.Errorf("category %s is not of kind %s", c, k)
	case c != "":
		k = c.Kind()
	}
	date, err := resolveDate(s.Date, today)
	if err != nil {
		return resolved{}, err
	}
	return resolved{Amount: amount, Category: c, Kind: k, Date: date, Comment: s.Comment, Payment: s.Payment}, nil
}

var weekdayNames = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
	"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
}

func resolveDate(spec string, today time.Time) (time.Time, error) {
	switch spec {
	case "", "today":
		return today, nil
	case "yesterday":
		return today.AddDate(0, 0, -1), nil
	}
	if wd, ok := weekdayNames[spec]; ok {
		return today.AddDate(0, 0, -((int(today.Weekday()) - int(wd) + 7) % 7)), nil
	}
	if n, ok := strings.CutPrefix(spec, "day:"); ok {
		d, err := strconv.Atoi(n)
		if err != nil {
			return time.Time{}, fmt.Errorf("date %q: %w", spec, err)
		}
		t := time.Date(today.Year(), today.Month(), d, 0, 0, 0, 0, time.UTC)
		if t.After(today) { // "3 числа" before the 3rd means last month
			t = t.AddDate(0, -1, 0)
		}
		return t, nil
	}
	if n, err := strconv.Atoi(spec); err == nil && n <= 0 {
		return today.AddDate(0, 0, n), nil
	}
	return time.Time{}, fmt.Errorf("unknown date %q", spec)
}

func (r resolved) String() string {
	what := string(r.Category)
	if what == "" {
		what = "any " + string(r.Kind)
	}
	s := fmt.Sprintf("%s %s %s", r.Amount, what, r.Date.Format(time.DateOnly))
	if r.Comment != "" {
		s += " «" + r.Comment + "»"
	}
	if r.Payment != "" {
		s += " → " + r.Payment
	}
	return s
}
