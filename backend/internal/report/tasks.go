package report

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/game"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/tasks"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

const (
	kindTask = "task" // the period is "<task id>:<date>"

	// taskTick: a reminder must come on the minute, unlike the morning message.
	taskTick = time.Minute
	// taskStale: a reminder missed for this long is no use any more.
	taskStale = 12 * time.Hour
)

// TaskList is what the reminders read; *tasks.Service implements it.
type TaskList interface {
	Timed(ctx context.Context) ([]tasks.Task, error)
}

// TaskReminders sends a reminder at the time of each task.
type TaskReminders struct {
	tasks   TaskList
	members Members
	runs    Runs
	say     ActionSend
	t       texts.Bot
	loc     *time.Location
	now     func() time.Time
	log     *slog.Logger
}

func NewTaskReminders(list TaskList, members Members, runs Runs, say ActionSend, t texts.Bot, loc *time.Location, log *slog.Logger) *TaskReminders {
	return &TaskReminders{tasks: list, members: members, runs: runs, say: say, t: t, loc: loc, now: time.Now, log: log}
}

// Run checks every minute until ctx is cancelled.
func (r *TaskReminders) Run(ctx context.Context) {
	t := time.NewTicker(taskTick)
	defer t.Stop()
	for {
		r.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick sends what has come due.
func (r *TaskReminders) Tick(ctx context.Context) {
	list, err := r.tasks.Timed(ctx)
	if err != nil {
		r.log.ErrorContext(ctx, "task reminders", "err", err)
		return
	}
	if len(list) == 0 {
		return
	}
	members, err := r.members.All(ctx)
	if err != nil {
		r.log.ErrorContext(ctx, "task reminders: list users", "err", err)
		return
	}
	people := make(map[int64]user.User, len(members))
	for _, m := range members {
		if m.Consented() && !m.Blocked() {
			people[m.TelegramID] = m
		}
	}
	now := r.now()
	for _, task := range list {
		m, ok := people[task.UserID]
		if !ok {
			continue
		}
		at, ok := task.Moment(m.Location(r.loc))
		if !ok || at.After(now) || now.Sub(at) > taskStale {
			continue
		}
		period := strconv.FormatInt(task.ID, 10) + ":" + task.DueOn.Format(time.DateOnly)
		claimed, err := r.runs.Claim(ctx, kindTask, period, task.UserID)
		if err != nil || !claimed {
			continue
		}
		rows := [][]game.Action{{{Label: r.t.TaskDoneButton, Data: game.ActTaskDone + ":" + strconv.FormatInt(task.ID, 10)}}}
		if err := r.say(ctx, task.UserID, texts.Fill(r.t.TaskReminder, "title", task.Title), rows); err != nil {
			r.log.WarnContext(ctx, "task reminder", "user_id", task.UserID, "err", err)
			if relErr := r.runs.Release(ctx, kindTask, period, task.UserID); relErr != nil {
				r.log.ErrorContext(ctx, "task reminder: release claim", "err", relErr)
			}
		}
	}
}

// taskLine is the tasks of the day in the morning message.
func taskLine(t texts.Bot, list []tasks.Task) string {
	titles := make([]string, len(list))
	for i, task := range list {
		titles[i] = task.Title
	}
	return texts.Fill(t.MorningTasks, "tasks", strings.Join(titles, ", "))
}
