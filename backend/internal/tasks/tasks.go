// Package tasks is the calendar of plans: what to do and when, with a reminder
// at the time. A repeating task keeps one row, and its date moves on when done.
package tasks

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

// Repeats of a task.
const (
	RepeatNone     = "none"
	RepeatDaily    = "daily"
	RepeatWeekdays = "weekdays"
	RepeatWeekly   = "weekly"
	RepeatMonthly  = "monthly"
)

var repeats = []string{RepeatNone, RepeatDaily, RepeatWeekdays, RepeatWeekly, RepeatMonthly}

const (
	maxTitle = 200
	maxRange = 62 // days a calendar may ask for at once
	maxOpen  = 200
)

var clock = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// ErrNotFound: no such task among the user's own.
var ErrNotFound = errors.New("task not found")

// Task is something planned.
type Task struct {
	ID        int64
	UserID    int64
	Title     string
	DueOn     time.Time // the next date it is due
	DueTime   string    // "HH:MM" in the user's timezone; empty: no reminder
	Repeat    string
	GoalID    *int64 // done, it suggests a deposit to this goal
	DoneAt    *time.Time
	CreatedAt time.Time
}

// Store persists tasks within one user's data, except Open.
type Store interface {
	List(ctx context.Context, userID int64) ([]Task, error)
	Get(ctx context.Context, userID, id int64) (Task, error) // ErrNotFound
	Create(ctx context.Context, t Task) (Task, error)
	Update(ctx context.Context, t Task) (Task, error)
	Delete(ctx context.Context, userID, id int64) error
	// Open lists the tasks of every user not done yet.
	Open(ctx context.Context) ([]Task, error)
}

// Service manages tasks.
type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

func invalid(field, message string) error {
	return &ledger.ValidationError{Field: field, Message: message}
}

func (t *Task) normalize(today time.Time) error {
	t.Title = strings.TrimSpace(t.Title)
	if t.Title == "" || utf8.RuneCountInString(t.Title) > maxTitle {
		return invalid("title", "from 1 to 200 characters")
	}
	if t.DueOn.IsZero() {
		t.DueOn = today
	}
	if t.DueOn.Before(today.AddDate(-1, 0, 0)) {
		return invalid("due_on", "too far in the past")
	}
	if t.DueTime != "" && !clock.MatchString(t.DueTime) {
		return invalid("due_time", "HH:MM, like 09:30")
	}
	if t.Repeat == "" {
		t.Repeat = RepeatNone
	}
	if !slices.Contains(repeats, t.Repeat) {
		return invalid("repeat", "unknown repeat")
	}
	return nil
}

// Create adds a task; today is the user's date.
func (s *Service) Create(ctx context.Context, t Task, today time.Time) (Task, error) {
	if err := t.normalize(today); err != nil {
		return Task{}, err
	}
	list, err := s.store.List(ctx, t.UserID)
	if err != nil {
		return Task{}, err
	}
	if len(list) >= maxOpen {
		return Task{}, invalid("tasks", "too many tasks")
	}
	return s.store.Create(ctx, t)
}

// Patch holds what can change; nil fields stay, an empty DueTime removes the reminder.
type Patch struct {
	Title   *string
	DueOn   *time.Time
	DueTime *string
	Repeat  *string
}

func (s *Service) Update(ctx context.Context, userID, id int64, p Patch, today time.Time) (Task, error) {
	t, err := s.store.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if p.Title != nil {
		t.Title = *p.Title
	}
	if p.DueOn != nil {
		t.DueOn = *p.DueOn
	}
	if p.DueTime != nil {
		t.DueTime = *p.DueTime
	}
	if p.Repeat != nil {
		t.Repeat = *p.Repeat
	}
	if err := t.normalize(today); err != nil {
		return Task{}, err
	}
	return s.store.Update(ctx, t)
}

func (s *Service) Delete(ctx context.Context, userID, id int64) error {
	return s.store.Delete(ctx, userID, id)
}

// Done marks a task done. A repeating one moves on to its next date.
func (s *Service) Done(ctx context.Context, userID, id int64, today, at time.Time) (Task, error) {
	t, err := s.store.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.Repeat == RepeatNone {
		t.DoneAt = &at
	} else {
		for !t.DueOn.After(today) {
			t.DueOn = next(t.DueOn, t.Repeat)
		}
	}
	return s.store.Update(ctx, t)
}

// next is the following date of a repeating task.
func next(d time.Time, repeat string) time.Time {
	switch repeat {
	case RepeatWeekly:
		return d.AddDate(0, 0, 7)
	case RepeatMonthly:
		first := time.Date(d.Year(), d.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		last := first.AddDate(0, 1, -1).Day()
		return first.AddDate(0, 0, min(d.Day(), last)-1)
	case RepeatWeekdays:
		d = d.AddDate(0, 0, 1)
		for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			d = d.AddDate(0, 0, 1)
		}
		return d
	default:
		return d.AddDate(0, 0, 1)
	}
}

// Occurrence is a task on a date of the calendar.
type Occurrence struct {
	Task
	Date time.Time
	Done bool
}

// Calendar lists the tasks between two dates, a repeating one on each of its dates.
func (s *Service) Calendar(ctx context.Context, userID int64, from, to time.Time) ([]Occurrence, error) {
	if to.Before(from) || to.Sub(from) > maxRange*24*time.Hour {
		return nil, invalid("to", "at most two months at a time")
	}
	list, err := s.store.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := []Occurrence{}
	for _, t := range list {
		if t.Repeat == RepeatNone {
			if !t.DueOn.Before(from) && !t.DueOn.After(to) {
				out = append(out, Occurrence{Task: t, Date: t.DueOn, Done: t.DoneAt != nil})
			}
			continue
		}
		for d := t.DueOn; !d.After(to); d = next(d, t.Repeat) {
			if !d.Before(from) {
				out = append(out, Occurrence{Task: t, Date: d})
			}
		}
	}
	slices.SortFunc(out, func(a, b Occurrence) int {
		if c := a.Date.Compare(b.Date); c != 0 {
			return c
		}
		return strings.Compare(a.DueTime, b.DueTime)
	})
	return out, nil
}

// Today lists what is due by a date and not done: what was missed stays on the list.
func (s *Service) Today(ctx context.Context, userID int64, day time.Time) ([]Task, error) {
	list, err := s.store.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []Task
	for _, t := range list {
		if t.DoneAt == nil && !t.DueOn.After(day) {
			out = append(out, t)
		}
	}
	return out, nil
}

// Timed lists the tasks of every user waiting for a reminder.
func (s *Service) Timed(ctx context.Context) ([]Task, error) {
	open, err := s.store.Open(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(open, func(t Task) bool { return t.DueTime == "" }), nil
}

// Users lists who has something due by a date: they get the morning message.
func (s *Service) Users(ctx context.Context, day time.Time) ([]int64, error) {
	open, err := s.store.Open(ctx)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, t := range open {
		if !t.DueOn.After(day) {
			ids = append(ids, t.UserID)
		}
	}
	return ids, nil
}

// Moment is when the reminder of a task is due in a timezone.
func (t Task) Moment(loc *time.Location) (time.Time, bool) {
	if t.DueTime == "" {
		return time.Time{}, false
	}
	clock, err := time.Parse("15:04", t.DueTime)
	if err != nil {
		return time.Time{}, false
	}
	y, m, d := t.DueOn.Date()
	return time.Date(y, m, d, clock.Hour(), clock.Minute(), 0, 0, loc), true
}
