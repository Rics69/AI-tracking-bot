package tasks_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/memory"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/tasks"
)

const anna int64 = 1

var (
	ctx   = context.Background()
	today = time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) // a Wednesday
)

func TestRepeatingTaskMovesOn(t *testing.T) {
	tests := []struct {
		repeat string
		start  time.Time
		want   time.Time
	}{
		{tasks.RepeatDaily, today, today.AddDate(0, 0, 1)},
		{tasks.RepeatWeekly, today, today.AddDate(0, 0, 7)},
		{tasks.RepeatMonthly, today, today.AddDate(0, 1, 0)},
		{tasks.RepeatMonthly, time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)},
		{tasks.RepeatWeekdays, time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.repeat, func(t *testing.T) {
			s := tasks.NewService(memory.NewTasks())
			task, err := s.Create(ctx, tasks.Task{UserID: anna, Title: "a", DueOn: tt.start, Repeat: tt.repeat}, tt.start)
			if err != nil {
				t.Fatal(err)
			}
			done, err := s.Done(ctx, anna, task.ID, tt.start, tt.start)
			if err != nil || done.DoneAt != nil || !done.DueOn.Equal(tt.want) {
				t.Errorf("done = %+v, %v, want %s", done, err, tt.want.Format(time.DateOnly))
			}
		})
	}
}

func TestDone(t *testing.T) {
	s := tasks.NewService(memory.NewTasks())
	task, _ := s.Create(ctx, tasks.Task{UserID: anna, Title: "a", DueOn: today}, today)
	done, err := s.Done(ctx, anna, task.ID, today, today)
	if err != nil || done.DoneAt == nil {
		t.Fatalf("done = %+v, %v", done, err)
	}
	if list, _ := s.Today(ctx, anna, today); len(list) != 0 {
		t.Errorf("a done task is still today's: %+v", list)
	}
	if _, err := s.Done(ctx, 2, task.ID, today, today); !errors.Is(err, tasks.ErrNotFound) {
		t.Errorf("someone else's task: %v", err)
	}
}

func TestCalendarAndToday(t *testing.T) {
	s := tasks.NewService(memory.NewTasks())
	_, _ = s.Create(ctx, tasks.Task{UserID: anna, Title: "раз", DueOn: today.AddDate(0, 0, 2)}, today)
	_, _ = s.Create(ctx, tasks.Task{UserID: anna, Title: "неделя", DueOn: today, Repeat: tasks.RepeatWeekly}, today)
	list, err := s.Calendar(ctx, anna, today, today.AddDate(0, 0, 14))
	if err != nil || len(list) != 4 || list[0].Title != "неделя" || list[1].Title != "раз" {
		t.Fatalf("calendar = %+v, %v", list, err)
	}
	if _, err := s.Calendar(ctx, anna, today, today.AddDate(0, 6, 0)); err == nil {
		t.Error("half a year at once")
	}
	got, _ := s.Today(ctx, anna, today)
	if len(got) != 1 || got[0].Title != "неделя" {
		t.Errorf("today = %+v", got)
	}
	// What was missed stays on the list.
	if got, _ = s.Today(ctx, anna, today.AddDate(0, 0, 3)); len(got) != 2 {
		t.Errorf("later = %+v", got)
	}
}

func TestValidation(t *testing.T) {
	s := tasks.NewService(memory.NewTasks())
	for name, task := range map[string]tasks.Task{
		"no title":     {UserID: anna, DueOn: today},
		"a bad time":   {UserID: anna, Title: "a", DueOn: today, DueTime: "9:30"},
		"a bad repeat": {UserID: anna, Title: "a", DueOn: today, Repeat: "yearly"},
	} {
		if _, err := s.Create(ctx, task, today); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestMoment(t *testing.T) {
	loc := time.FixedZone("UTC+3", 3*3600)
	at, ok := tasks.Task{DueOn: today, DueTime: "09:30"}.Moment(loc)
	if !ok || !at.Equal(time.Date(2026, 11, 18, 6, 30, 0, 0, time.UTC)) {
		t.Errorf("moment = %s, %v", at, ok)
	}
	if _, ok := (tasks.Task{DueOn: today}).Moment(loc); ok {
		t.Error("no time, no reminder")
	}
}
