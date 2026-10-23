package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
)

// Tools of the optional part: the daily limit, goals and regular payments.
const (
	toolGetBudget     = "get_budget"
	toolSetPayday     = "set_payday"
	toolAddRegular    = "add_regular_payment"
	toolSetGoal       = "set_goal"
	toolDepositToGoal = "deposit_to_goal"
)

// Budget is the part of budget.Service the agent needs.
type Budget interface {
	Plan(ctx context.Context, userID int64) (*budget.Plan, error)
	SetPlan(ctx context.Context, p budget.Plan) (budget.Plan, error)
	Budget(ctx context.Context, userID int64, today time.Time) (*budget.Budget, error)
	Goals(ctx context.Context, userID int64) ([]budget.Goal, error)
	CreateGoal(ctx context.Context, g budget.Goal, today time.Time) (budget.Goal, error)
	UpdateGoal(ctx context.Context, userID, id int64, p budget.GoalPatch, today time.Time) (budget.Goal, error)
	Deposit(ctx context.Context, userID, goalID int64, amount decimal.Decimal, date *time.Time, today time.Time) (budget.Goal, error)
	Payments(ctx context.Context, userID int64) ([]budget.RegularPayment, error)
	Payment(ctx context.Context, userID, id int64) (budget.RegularPayment, error)
	CreatePayment(ctx context.Context, p budget.RegularPayment) (budget.RegularPayment, error)
}

func budgetToolDefs(today time.Time) []llm.Tool {
	d := func(t time.Time) string { return t.Format(time.DateOnly) }
	return []llm.Tool{
		{
			Name: toolGetBudget,
			Description: "Узнать дневной лимит: сколько можно потратить сегодня, сколько осталось до зарплаты, " +
				"ближайшие регулярные платежи и прогресс целей.",
			Parameters: json.RawMessage(`{"type": "object", "properties": {}}`),
			Examples: []llm.ToolExample{
				{Request: "сколько можно потратить сегодня", Params: map[string]any{}},
				{Request: "сколько осталось до зарплаты", Params: map[string]any{}},
			},
		},
		{
			Name: toolSetPayday,
			Description: "Задать дни зарплаты — от них считается дневной лимит. Можно сразу указать ожидаемый доход за период " +
				"и сколько откладывать на цели за период. Непереданные поля не меняются.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "days": {"type": "array", "items": {"type": "integer"}, "description": "Один или два дня месяца, когда приходят деньги: [5] или [5, 20]"},
    "expected_income": {"type": "number", "description": "Сколько обычно приходит за один период между зарплатами, в рублях"},
    "goal_contribution": {"type": "number", "description": "Сколько откладывать на цели за период, в рублях"}
  },
  "required": ["days"]
}`),
			Examples: []llm.ToolExample{
				{Request: "зарплата приходит 5 и 20 числа", Params: map[string]any{"days": []int{5, 20}}},
				{Request: "получаю 80к десятого числа", Params: map[string]any{"days": []int{10}, "expected_income": 80000}},
			},
		},
		{
			Name: toolAddRegular,
			Description: "Добавить регулярный платёж, который повторяется каждый месяц в один день: аренда, связь, подписка. " +
				"Это не трата: трата записывается, когда платёж оплачен.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "title": {"type": "string", "description": "Название, как сказал пользователь: «Аренда», «Интернет»"},
    "amount": {"type": "number", "description": "Сумма в рублях"},
    "day": {"type": "integer", "description": "День месяца, 1–31"},
    "category": ` + expenseCategorySchema + `
  },
  "required": ["title", "amount", "day", "category"]
}`),
			Examples: []llm.ToolExample{
				{Request: "аренда 45000 каждое 1 число", Params: map[string]any{"title": "Аренда", "amount": 45000, "day": 1, "category": "housing"}},
				{Request: "подписка на музыку 299 ежемесячно 15-го", Params: map[string]any{"title": "Подписка на музыку", "amount": 299, "day": 15, "category": "entertainment"}},
			},
		},
		{
			Name:        toolSetGoal,
			Description: "Поставить цель накопления или изменить сумму и срок существующей цели с тем же названием.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "title": {"type": "string", "description": "На что копит пользователь: «Отпуск», «Ноутбук»"},
    "target": {"type": "number", "description": "Сколько нужно накопить, в рублях"},
    "deadline": {"type": "string", "description": "К какой дате, YYYY-MM-DD; не указывай, если пользователь не назвал срок"}
  },
  "required": ["title", "target"]
}`),
			Examples: []llm.ToolExample{
				{Request: "хочу накопить 100к на отпуск", Params: map[string]any{"title": "Отпуск", "target": 100000}},
				{Request: "коплю на ноутбук 90000 к концу года", Params: map[string]any{"title": "Ноутбук", "target": 90000,
					"deadline": d(time.Date(today.Year(), 12, 31, 0, 0, 0, 0, time.UTC))}},
			},
		},
		{
			Name:        toolDepositToGoal,
			Description: "Отложить деньги на цель. Отрицательная сумма — забрать деньги из цели. Отложенные деньги — не трата.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "goal_id": {"type": "integer", "description": "id цели из списка целей в инструкциях; можно не указывать, если цель одна"},
    "amount": {"type": "number", "description": "Сколько отложить, в рублях; со знаком минус — сколько забрать"},
    "date": {"type": "string", "description": "Дата YYYY-MM-DD; если пользователь не назвал день — сегодняшняя"}
  },
  "required": ["amount"]
}`),
			Examples: []llm.ToolExample{
				{Request: "отложил 5000 на отпуск (цель «Отпуск», id 2)", Params: map[string]any{"goal_id": 2, "amount": 5000, "date": d(today)}},
				{Request: "забрал 2000 из копилки на отпуск (id 2)", Params: map[string]any{"goal_id": 2, "amount": -2000, "date": d(today)}},
			},
		},
	}
}

// moneyContext is the user's goals and regular payments for the system prompt,
// so the model links "аренда 45000" to the rent and "на отпуск" to the goal.
type moneyContext struct {
	plan     *budget.Plan
	goals    []budget.Goal
	payments []budget.RegularPayment
}

func (a *Agent) loadMoneyContext(ctx context.Context, userID int64) *moneyContext {
	if a.budget == nil {
		return nil
	}
	var (
		mc  moneyContext
		err error
	)
	// Without the lists the agent still works, it only links less: a storage
	// hiccup must not cost the user their reply.
	if mc.plan, err = a.budget.Plan(ctx, userID); err != nil {
		a.log.WarnContext(ctx, "load plan", "user_id", userID, "err", err)
	}
	if mc.goals, err = a.budget.Goals(ctx, userID); err != nil {
		a.log.WarnContext(ctx, "load goals", "user_id", userID, "err", err)
	}
	if mc.payments, err = a.budget.Payments(ctx, userID); err != nil {
		a.log.WarnContext(ctx, "load regular payments", "user_id", userID, "err", err)
	}
	return &mc
}

func (mc *moneyContext) String() string {
	var b strings.Builder
	b.WriteString("\n\nДеньги пользователя (лимит, цели и регулярные платежи необязательны):\n")
	if mc.plan == nil {
		b.WriteString("- день зарплаты не задан, дневного лимита нет\n")
	} else {
		days := make([]string, len(mc.plan.Paydays))
		for i, d := range mc.plan.Paydays {
			days[i] = fmt.Sprint(d)
		}
		fmt.Fprintf(&b, "- дни зарплаты: %s числа\n", strings.Join(days, " и "))
	}
	if len(mc.payments) == 0 {
		b.WriteString("- регулярных платежей нет\n")
	} else {
		b.WriteString("- регулярные платежи (id: название — сумма, день месяца):\n")
		for _, p := range mc.payments {
			fmt.Fprintf(&b, "  %d: %s — %s ₽, %d-го числа\n", p.ID, p.Title, p.Amount.StringFixedBank(2), p.Day)
		}
	}
	if len(mc.goals) == 0 {
		b.WriteString("- целей нет\n")
	} else {
		b.WriteString("- цели (id: название — накоплено из суммы):\n")
		for _, g := range mc.goals {
			fmt.Fprintf(&b, "  %d: %s — %s из %s ₽\n", g.ID, g.Title, g.Saved.StringFixedBank(2), g.Target.StringFixedBank(2))
		}
	}
	return b.String()
}

// runBudgetTool executes a tool of the optional part; without a budget
// service the tools are not offered, so a call is the model's mistake.
func (a *Agent) runBudgetTool(ctx context.Context, t turn, call *llm.ToolCall) (any, error) {
	if a.budget == nil {
		return nil, badArgs("unknown function %q", call.Name)
	}
	switch call.Name {
	case toolGetBudget:
		return a.getBudget(ctx, t)
	case toolSetPayday:
		return a.setPayday(ctx, t, call.Arguments)
	case toolAddRegular:
		return a.addRegular(ctx, t, call.Arguments)
	case toolSetGoal:
		return a.setGoal(ctx, t, call.Arguments)
	default:
		return a.deposit(ctx, t, call.Arguments)
	}
}

func (a *Agent) getBudget(ctx context.Context, t turn) (any, error) {
	b, err := a.budget.Budget(ctx, t.userID, t.today)
	if err != nil {
		return nil, err
	}
	goals, err := a.budget.Goals(ctx, t.userID)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return map[string]any{
			"configured": false,
			"hint":       "the daily limit needs the user's payday: explain it and ask for the day of the month, then call set_payday",
			"goals":      t.goalItems(goals),
		}, nil
	}
	if !b.Ready {
		return map[string]any{
			"configured":       true,
			"ready":            false,
			"next_payday_text": humanDate(b.NextPayday, t.today),
			"hint": "no income is recorded or expected since the last payday: ask the user to record the income they got " +
				"or to name the expected income (set_payday with expected_income)",
			"goals": t.goalItems(goals),
		}, nil
	}
	type due struct {
		Title    string      `json:"title"`
		Amount   json.Number `json:"amount"`
		DateText string      `json:"date_text"`
	}
	upcoming := make([]due, len(b.Upcoming))
	for i, u := range b.Upcoming {
		upcoming[i] = due{Title: u.Payment.Title, Amount: money(u.Payment.Amount), DateText: humanDate(u.Date, t.today)}
	}
	// The answer comes first and flat: the model quotes the first numbers it sees.
	return struct {
		Configured       bool        `json:"configured"`
		LimitToday       json.Number `json:"limit_today"`
		SpentToday       json.Number `json:"spent_today"`
		LeftToday        json.Number `json:"left_today"`
		LeftUntilPayday  json.Number `json:"left_until_payday"`
		NextPaydayText   string      `json:"next_payday_text"`
		DaysLeft         int         `json:"days_left_including_today"`
		Income           json.Number `json:"income_of_period"`
		IncomeIsExpected bool        `json:"income_is_expected"`
		Reserved         json.Number `json:"reserved_for_regular_payments"`
		Saved            json.Number `json:"put_aside_for_goals"`
		Upcoming         []due       `json:"upcoming_payments"`
		Goals            []goalItem  `json:"goals"`
	}{
		true, money(b.Limit), money(b.SpentToday), money(b.Left()), money(b.Remaining()),
		humanDate(b.NextPayday, t.today), b.DaysLeft, money(b.Income), b.IncomeExpected,
		money(b.Payments), money(b.Savings), upcoming, t.goalItems(goals),
	}, nil
}

type setPaydayArgs struct {
	Days             []json.Number `json:"days"`
	ExpectedIncome   json.Number   `json:"expected_income"`
	GoalContribution json.Number   `json:"goal_contribution"`
}

func (a *Agent) setPayday(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	var args setPaydayArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	if len(args.Days) == 0 {
		return nil, badArgs("days is required: one or two days of the month")
	}
	plan := budget.Plan{UserID: t.userID, ReminderHour: budget.DefaultReminderHour, Reminders: true}
	if old, err := a.budget.Plan(ctx, t.userID); err != nil {
		return nil, err
	} else if old != nil {
		plan = *old
	}
	plan.Paydays = make([]int, len(args.Days))
	for i, n := range args.Days {
		d, err := n.Int64()
		if err != nil {
			return nil, badArgs("days must be integers from 1 to 31, got %q", n)
		}
		plan.Paydays[i] = int(d)
	}
	var err error
	if args.ExpectedIncome != "" {
		if plan.ExpectedIncome, err = parseAmount(args.ExpectedIncome, true); err != nil {
			return nil, err
		}
	}
	if args.GoalContribution != "" {
		if plan.GoalContribution, err = parseAmount(args.GoalContribution, true); err != nil {
			return nil, err
		}
	}
	if _, err := a.budget.SetPlan(ctx, plan); err != nil {
		return nil, err
	}
	// The reply should say what the limit is now.
	return a.getBudget(ctx, t)
}

type addRegularArgs struct {
	Title    string      `json:"title"`
	Amount   json.Number `json:"amount"`
	Day      json.Number `json:"day"`
	Category string      `json:"category"`
}

func (a *Agent) addRegular(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	var args addRegularArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	amount, err := parseAmount(args.Amount, true)
	if err != nil {
		return nil, err
	}
	day, err := args.Day.Int64()
	if err != nil {
		return nil, badArgs("day must be a day of the month from 1 to 31, got %q", args.Day)
	}
	category, err := parseCategory(args.Category, false)
	if err != nil {
		return nil, err
	}
	p := budget.RegularPayment{UserID: t.userID, Title: args.Title, Amount: *amount, Day: int(day)}
	if category != nil {
		p.Category = *category
	}
	p, err = a.budget.CreatePayment(ctx, p)
	if err != nil {
		return nil, err
	}
	return map[string]any{"added": map[string]any{
		"id": p.ID, "title": p.Title, "amount": money(p.Amount), "day_of_month": p.Day,
		"category": p.Category.Label(), "next_date_text": humanDate(p.Due(t.today), t.today),
	}}, nil
}

type setGoalArgs struct {
	Title    string      `json:"title"`
	Target   json.Number `json:"target"`
	Deadline string      `json:"deadline"`
}

func (a *Agent) setGoal(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	var args setGoalArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	target, err := parseAmount(args.Target, true)
	if err != nil {
		return nil, err
	}
	deadline, err := parseDate("deadline", args.Deadline)
	if err != nil {
		return nil, err
	}
	goals, err := a.budget.Goals(ctx, t.userID)
	if err != nil {
		return nil, err
	}
	for _, g := range goals {
		if strings.EqualFold(strings.TrimSpace(g.Title), strings.TrimSpace(args.Title)) {
			updated, err := a.budget.UpdateGoal(ctx, t.userID, g.ID, budget.GoalPatch{Target: target, Deadline: deadline}, t.today)
			if err != nil {
				return nil, err
			}
			return map[string]any{"updated": t.goalItem(updated)}, nil
		}
	}
	created, err := a.budget.CreateGoal(ctx, budget.Goal{UserID: t.userID, Title: args.Title, Target: *target, Deadline: deadline}, t.today)
	if err != nil {
		return nil, err
	}
	return map[string]any{"created": t.goalItem(created)}, nil
}

type depositArgs struct {
	GoalID json.Number `json:"goal_id"`
	Amount json.Number `json:"amount"`
	Date   string      `json:"date"`
}

func (a *Agent) deposit(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	var args depositArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	amount, err := parseAmount(args.Amount, true)
	if err != nil {
		return nil, err
	}
	date, err := parseDate("date", args.Date)
	if err != nil {
		return nil, err
	}
	var goalID int64
	if args.GoalID != "" {
		if goalID, err = args.GoalID.Int64(); err != nil {
			return nil, badArgs("goal_id must be an id from the list of goals")
		}
	} else {
		goals, err := a.budget.Goals(ctx, t.userID)
		if err != nil {
			return nil, err
		}
		switch len(goals) {
		case 0:
			return nil, badArgs("the user has no goals: ask what they are saving for and how much, then call set_goal")
		case 1:
			goalID = goals[0].ID
		default:
			return nil, badArgs("the user has several goals: pass goal_id from the list, or ask which goal")
		}
	}
	g, err := a.budget.Deposit(ctx, t.userID, goalID, *amount, date, t.today)
	if err != nil {
		return nil, err
	}
	return map[string]any{"goal": t.goalItem(g), "deposited": money(*amount)}, nil
}

// goalItem is the compact representation of a goal shown to the model.
type goalItem struct {
	ID       int64       `json:"id"`
	Title    string      `json:"title"`
	Saved    json.Number `json:"saved"`
	Target   json.Number `json:"target"`
	Left     json.Number `json:"left"`
	Reached  bool        `json:"reached"`
	Deadline string      `json:"deadline_text,omitempty"`
}

func (t turn) goalItem(g budget.Goal) goalItem {
	it := goalItem{ID: g.ID, Title: g.Title, Saved: money(g.Saved), Target: money(g.Target), Left: money(g.Left()),
		Reached: !g.Left().IsPositive()}
	if g.Deadline != nil {
		it.Deadline = humanDate(*g.Deadline, t.today)
	}
	return it
}

func (t turn) goalItems(goals []budget.Goal) []goalItem {
	out := make([]goalItem, len(goals))
	for i, g := range goals {
		out[i] = t.goalItem(g)
	}
	return out
}

// checkRegularPayment makes sure an expense links to one of the user's own payments.
func (a *Agent) checkRegularPayment(ctx context.Context, t turn, raw json.Number, kind ledger.Kind) (*int64, error) {
	if raw == "" {
		return nil, nil
	}
	if a.budget == nil || kind != ledger.KindExpense {
		return nil, badArgs("regular_payment_id is only for expenses that pay a regular payment")
	}
	id, err := raw.Int64()
	if err != nil {
		return nil, badArgs("regular_payment_id must be an id from the list of regular payments")
	}
	if _, err := a.budget.Payment(ctx, t.userID, id); err != nil {
		return nil, err
	}
	return &id, nil
}
