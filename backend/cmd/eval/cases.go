package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/expense"
)

// Case is one eval scenario: a dialog with the agent and what must be true after it.
type Case struct {
	Name   string        `yaml:"name"`
	Tags   []string      `yaml:"tags"`
	Seed   []ExpenseSpec `yaml:"seed"`  // the user's expenses before the dialog, in creation order
	Turns  []string      `yaml:"turns"` // user messages, sent one by one in one conversation
	Expect Expect        `yaml:"expect"`
}

// Expect lists the checks of a case. Empty fields are not checked.
type Expect struct {
	// Expenses is the exact final state of the user's expenses (order does not matter).
	Expenses *[]ExpenseSpec `yaml:"expenses"`
	// Unchanged means the user's expenses must equal the seed.
	Unchanged bool `yaml:"unchanged"`
	// Tools must each be called at least once.
	Tools []string `yaml:"tools"`
	// NoTools means the agent must answer without calling any tool.
	NoTools bool `yaml:"no_tools"`
	// ReplyContains / ReplyNotContains are checked against the last reply,
	// case-insensitively and ignoring whitespace ("1200" matches "1 200 ₽").
	ReplyContains    []string `yaml:"reply_contains"`
	ReplyNotContains []string `yaml:"reply_not_contains"`
}

// ExpenseSpec describes an expense in a case file.
type ExpenseSpec struct {
	Amount   string `yaml:"amount"`
	Category string `yaml:"category"`
	// Date is relative to the day the eval runs: today, yesterday, -N (days ago),
	// a weekday name (the most recent one, today included) or day:N (N-th of this month).
	Date string `yaml:"date"`
	// Comment must be contained in the actual comment (case-insensitive); empty matches any.
	Comment string `yaml:"comment"`
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

// resolved is an ExpenseSpec with concrete values.
type resolved struct {
	Amount   decimal.Decimal
	Category expense.Category
	Date     time.Time
	Comment  string
}

func (s ExpenseSpec) resolve(today time.Time) (resolved, error) {
	amount, err := decimal.NewFromString(s.Amount)
	if err != nil {
		return resolved{}, fmt.Errorf("amount %q: %w", s.Amount, err)
	}
	c := expense.Category(s.Category)
	if !c.Valid() {
		return resolved{}, fmt.Errorf("unknown category %q", s.Category)
	}
	date, err := resolveDate(s.Date, today)
	if err != nil {
		return resolved{}, err
	}
	return resolved{Amount: amount, Category: c, Date: date, Comment: s.Comment}, nil
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
	s := fmt.Sprintf("%s %s %s", r.Amount, r.Category, r.Date.Format(time.DateOnly))
	if r.Comment != "" {
		s += " «" + r.Comment + "»"
	}
	return s
}
