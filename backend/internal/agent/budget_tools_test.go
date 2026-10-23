package agent

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/memory"
)

// fixedClock says today is the test's Friday.
type fixedClock struct{}

func (fixedClock) TodayIn(*time.Location) time.Time { return today }

// budgetEnv runs the real ledger and budget services on memory storage.
type budgetEnv struct {
	ops    *memory.OperationRepository
	ledger *ledger.Service
	budget *budget.Service
}

func newBudgetEnv() *budgetEnv {
	ops := memory.NewOperationRepository()
	l := ledger.NewService(ops, time.UTC)
	return &budgetEnv{ops: ops, ledger: l, budget: budget.NewService(memory.NewBudget(ops), l)}
}

func (e *budgetEnv) agent(model llm.Client) *Agent {
	return New(model, e.ledger, fixedClock{}, NewHistory(6, time.Hour), Config{Location: time.UTC, Budget: e.budget},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func (e *budgetEnv) reply(t *testing.T, model *scriptedLLM, text string) {
	t.Helper()
	if _, err := e.agent(model).Reply(context.Background(), anna, text); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetToolsAreOptional(t *testing.T) {
	model := &scriptedLLM{responses: []llm.Response{call(toolGetBudget, `{}`), text("?")}}
	if _, err := newTestAgent(model, newMemExpenses()).Reply(context.Background(), anna, "какой лимит"); err != nil {
		t.Fatal(err)
	}
	for _, tool := range model.requests[0].Tools {
		if tool.Name == toolGetBudget {
			t.Error("budget tools are offered without a budget service")
		}
	}
	if sys := model.requests[0].Messages[0].Content; strings.Contains(sys, "get_budget") {
		t.Error("the prompt mentions the limit without a budget service")
	}
	if res := lastToolResult(t, model.requests[1]); !strings.Contains(res, "unknown function") {
		t.Errorf("tool result = %s", res)
	}
}

func TestSetPaydayAndGetBudget(t *testing.T) {
	e := newBudgetEnv()
	ctx := context.Background()
	if _, err := e.ledger.Create(ctx, ledger.CreateInput{UserID: anna.UserID, Amount: decimal.NewFromInt(26000),
		Category: ledger.CategorySalary, Date: ptrTime(today.AddDate(0, 0, -11)), Today: today}); err != nil {
		t.Fatal(err)
	}

	// Without a plan the model is told what to ask.
	model := &scriptedLLM{responses: []llm.Response{call(toolGetBudget, `{}`), text("Когда зарплата?")}}
	e.reply(t, model, "сколько можно потратить сегодня")
	if res := lastToolResult(t, model.requests[1]); !strings.Contains(res, `"configured":false`) || !strings.Contains(res, "set_payday") {
		t.Errorf("no plan: %s", res)
	}

	// Payday on the 5th: the period is Oct 5 – Nov 4, 20 days from the 16th.
	model = &scriptedLLM{responses: []llm.Response{call(toolSetPayday, `{"days": [5]}`), text("Готово")}}
	e.reply(t, model, "зарплата пятого")
	res := lastToolResult(t, model.requests[1])
	if !strings.Contains(res, `"limit_today":1300.00`) || !strings.Contains(res, `"next_payday_text":"5 ноября"`) {
		t.Errorf("after set_payday: %s", res)
	}
	plan, _ := e.budget.Plan(ctx, anna.UserID)
	if plan == nil || plan.Paydays[0] != 5 || !plan.Reminders || plan.ReminderHour != budget.DefaultReminderHour {
		t.Errorf("plan = %+v", plan)
	}
	// The plan is in the prompt of the next request.
	model = &scriptedLLM{responses: []llm.Response{text("ok")}}
	e.reply(t, model, "привет")
	if sys := model.requests[0].Messages[0].Content; !strings.Contains(sys, "дни зарплаты: 5 числа") {
		t.Errorf("prompt lacks the plan: %s", sys)
	}
}

func TestRegularPaymentIsLinkedByID(t *testing.T) {
	e := newBudgetEnv()
	model := &scriptedLLM{responses: []llm.Response{
		call(toolAddRegular, `{"title": "Аренда", "amount": 45000, "day": 1, "category": "housing"}`),
		text("Добавлено"),
	}}
	e.reply(t, model, "аренда 45000 каждое первое число")
	if res := lastToolResult(t, model.requests[1]); !strings.Contains(res, `"next_date_text":"1 ноября"`) {
		t.Errorf("add_regular_payment: %s", res)
	}
	if ops := e.ops.All(); len(ops) != 0 {
		t.Errorf("a regular payment is not an expense: %+v", ops)
	}

	payments, _ := e.budget.Payments(context.Background(), anna.UserID)
	id := payments[0].ID
	model = &scriptedLLM{responses: []llm.Response{
		call(toolAddExpense, `{"amount": 45000, "category": "housing", "comment": "аренда", "regular_payment_id": 999}`),
		call(toolAddExpense, `{"amount": 45000, "category": "housing", "comment": "аренда", "regular_payment_id": `+itoa(id)+`}`),
		text("Записано"),
	}}
	e.reply(t, model, "заплатил аренду 45000")
	if sys := model.requests[0].Messages[0].Content; !strings.Contains(sys, itoa(id)+": Аренда — 45000.00 ₽, 1-го числа") {
		t.Errorf("prompt lacks the payment: %s", sys)
	}
	if res := lastToolResult(t, model.requests[1]); !strings.Contains(res, "no such goal or regular payment") {
		t.Errorf("unknown payment id: %s", res)
	}
	ops := e.ops.All()
	if len(ops) != 1 || ops[0].RegularPaymentID == nil || *ops[0].RegularPaymentID != id {
		t.Errorf("expenses = %+v", ops)
	}
}

func TestGoalsAndDeposits(t *testing.T) {
	e := newBudgetEnv()
	model := &scriptedLLM{responses: []llm.Response{
		call(toolDepositToGoal, `{"amount": 5000}`),
		call(toolSetGoal, `{"title": "Отпуск", "target": 100000}`),
		call(toolDepositToGoal, `{"amount": 5000}`),
		call(toolDepositToGoal, `{"amount": -7000}`),
		call(toolSetGoal, `{"title": "отпуск", "target": 120000}`),
		text("Готово"),
	}}
	e.reply(t, model, "отложил 5000 на отпуск")
	results := make([]string, 0, 5)
	for _, req := range model.requests[1:] {
		results = append(results, lastToolResult(t, req))
	}
	if !strings.Contains(results[0], "no goals") {
		t.Errorf("deposit without goals: %s", results[0])
	}
	if !strings.Contains(results[2], `"saved":5000.00`) {
		t.Errorf("deposit: %s", results[2])
	}
	if !strings.Contains(results[3], "only 5000.00 is saved") {
		t.Errorf("taking back too much: %s", results[3])
	}
	if !strings.Contains(results[4], `"updated"`) || !strings.Contains(results[4], `"target":120000.00`) {
		t.Errorf("same title updates the goal: %s", results[4])
	}
	goals, _ := e.budget.Goals(context.Background(), anna.UserID)
	if len(goals) != 1 || !goals[0].Saved.Equal(decimal.NewFromInt(5000)) {
		t.Errorf("goals = %+v", goals)
	}
	if len(e.ops.All()) != 0 {
		t.Error("a deposit is not an expense")
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func itoa(n int64) string { return decimal.NewFromInt(n).String() }
