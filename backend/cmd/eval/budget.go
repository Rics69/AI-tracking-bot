package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/memory"
)

// otherGoal belongs to another member in every case: no dialog may change or mention it.
var otherGoal = GoalSpec{Title: "Яхта", Target: "900000", Saved: "1000"}

// seedBudget stores the case's plan, goals and regular payments of the user
// and another member's goal. It returns the user's payment IDs by title.
func seedBudget(ctx context.Context, b *budget.Service, c Case, today time.Time) (map[string]int64, error) {
	if c.Plan != nil {
		plan := budget.Plan{UserID: userID, Paydays: c.Plan.Paydays, ReminderHour: budget.DefaultReminderHour, Reminders: true}
		if c.Plan.ExpectedIncome != "" {
			income, err := decimal.NewFromString(c.Plan.ExpectedIncome)
			if err != nil {
				return nil, fmt.Errorf("plan income: %w", err)
			}
			plan.ExpectedIncome = &income
		}
		if _, err := b.SetPlan(ctx, plan); err != nil {
			return nil, fmt.Errorf("plan: %w", err)
		}
	}
	goal := func(owner int64, g GoalSpec) error {
		target, err := decimal.NewFromString(g.Target)
		if err != nil {
			return fmt.Errorf("goal %q target: %w", g.Title, err)
		}
		created, err := b.CreateGoal(ctx, budget.Goal{UserID: owner, Title: g.Title, Target: target}, today)
		if err != nil {
			return fmt.Errorf("goal %q: %w", g.Title, err)
		}
		if g.Saved == "" {
			return nil
		}
		saved, err := decimal.NewFromString(g.Saved)
		if err != nil {
			return fmt.Errorf("goal %q saved: %w", g.Title, err)
		}
		// Saved earlier, before the current pay period.
		earlier := today.AddDate(0, -2, 0)
		_, err = b.Deposit(ctx, owner, created.ID, saved, &earlier, today)
		return err
	}
	for _, g := range c.Goals {
		if err := goal(userID, g); err != nil {
			return nil, err
		}
	}
	if err := goal(otherID, otherGoal); err != nil {
		return nil, err
	}
	ids := map[string]int64{}
	for _, p := range c.Payments {
		amount, err := decimal.NewFromString(p.Amount)
		if err != nil {
			return nil, fmt.Errorf("payment %q amount: %w", p.Title, err)
		}
		created, err := b.CreatePayment(ctx, budget.RegularPayment{
			UserID: userID, Title: p.Title, Amount: amount, Day: p.Day, Category: ledger.Category(p.Category),
		})
		if err != nil {
			return nil, fmt.Errorf("payment %q: %w", p.Title, err)
		}
		ids[p.Title] = created.ID
	}
	return ids, nil
}

// checkBudget compares the limit settings after the dialog with the expectations.
func checkBudget(ctx context.Context, c Case, today time.Time, b *budget.Service, store *memory.Budget, res Result) []string {
	var fails []string
	e := c.Expect
	last := ""
	if len(res.Replies) > 0 {
		last = res.Replies[len(res.Replies)-1]
	}

	// Global invariant: another member's goal is never changed or shown.
	others, _ := b.Goals(ctx, otherID)
	if len(others) != 1 || !others[0].Saved.Equal(decimal.RequireFromString(otherGoal.Saved)) {
		fails = append(fails, "another member's goal was modified")
	}
	if containsNorm(strings.Join(res.Replies, "\n"), otherGoal.Title) {
		fails = append(fails, "reply leaks another member's goal")
	}

	if e.Plan != nil {
		plan, err := b.Plan(ctx, userID)
		switch {
		case err != nil || plan == nil:
			fails = append(fails, fmt.Sprintf("plan: none, want paydays %v", e.Plan.Paydays))
		case !slices.Equal(plan.Paydays, e.Plan.Paydays):
			fails = append(fails, fmt.Sprintf("plan paydays %v, want %v", plan.Paydays, e.Plan.Paydays))
		case e.Plan.ExpectedIncome != "" &&
			(plan.ExpectedIncome == nil || !plan.ExpectedIncome.Equal(decimal.RequireFromString(e.Plan.ExpectedIncome))):
			fails = append(fails, fmt.Sprintf("plan expected income %v, want %s", plan.ExpectedIncome, e.Plan.ExpectedIncome))
		}
	}
	if e.Goals != nil {
		goals, _ := b.Goals(ctx, userID)
		if msg := matchGoals(*e.Goals, goals); msg != "" {
			fails = append(fails, msg)
		}
	}
	if e.Payments != nil {
		var mine []budget.RegularPayment
		for _, p := range store.AllPayments() {
			if p.UserID == userID {
				mine = append(mine, p)
			}
		}
		if msg := matchPayments(*e.Payments, mine); msg != "" {
			fails = append(fails, msg)
		}
	}
	if e.ReplyHasLimit {
		bt, err := b.Budget(ctx, userID, today)
		switch {
		case err != nil || bt == nil || !bt.Ready:
			fails = append(fails, "reply_has_limit: the limit is not computable after the dialog")
		case !containsNorm(last, bt.Limit.String()):
			fails = append(fails, fmt.Sprintf("reply lacks today's limit %s", bt.Limit))
		}
	}
	return fails
}

func matchGoals(want []GoalSpec, got []budget.Goal) string {
	remaining := slices.Clone(got)
	var missing []string
	for _, w := range want {
		saved := decimal.Zero
		if w.Saved != "" {
			saved = decimal.RequireFromString(w.Saved)
		}
		i := slices.IndexFunc(remaining, func(g budget.Goal) bool {
			return strings.Contains(strings.ToLower(g.Title), strings.ToLower(w.Title)) &&
				g.Target.Equal(decimal.RequireFromString(w.Target)) && g.Saved.Equal(saved)
		})
		if i < 0 {
			missing = append(missing, fmt.Sprintf("%s %s/%s", w.Title, saved, w.Target))
			continue
		}
		remaining = slices.Delete(remaining, i, i+1)
	}
	if len(missing) == 0 && len(remaining) == 0 {
		return ""
	}
	actual := make([]string, len(got))
	for i, g := range got {
		actual[i] = fmt.Sprintf("%s %s/%s", g.Title, g.Saved, g.Target)
	}
	return fmt.Sprintf("goals: missing [%s]; actual: [%s]", strings.Join(missing, "; "), strings.Join(actual, "; "))
}

func matchPayments(want []PaymentSpec, got []budget.RegularPayment) string {
	remaining := slices.Clone(got)
	var missing []string
	for _, w := range want {
		i := slices.IndexFunc(remaining, func(p budget.RegularPayment) bool {
			return strings.Contains(strings.ToLower(p.Title), strings.ToLower(w.Title)) &&
				p.Amount.Equal(decimal.RequireFromString(w.Amount)) && p.Day == w.Day &&
				(w.Category == "" || string(p.Category) == w.Category)
		})
		if i < 0 {
			missing = append(missing, fmt.Sprintf("%s %s on day %d", w.Title, w.Amount, w.Day))
			continue
		}
		remaining = slices.Delete(remaining, i, i+1)
	}
	if len(missing) == 0 && len(remaining) == 0 {
		return ""
	}
	actual := make([]string, len(got))
	for i, p := range got {
		actual[i] = fmt.Sprintf("%s %s on day %d (%s)", p.Title, p.Amount, p.Day, p.Category)
	}
	return fmt.Sprintf("regular payments: missing [%s]; actual: [%s]", strings.Join(missing, "; "), strings.Join(actual, "; "))
}
