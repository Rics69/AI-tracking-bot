package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/debts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
)

const (
	toolAddDebt  = "add_debt"
	toolPayDebt  = "add_debt_payment"
	toolGetDebts = "get_debts"
)

// Debts is the part of debts.Service the agent needs.
type Debts interface {
	List(ctx context.Context, userID int64) ([]debts.Debt, decimal.Decimal, error)
	Create(ctx context.Context, d debts.Debt, today time.Time) (debts.Debt, error)
	ByName(ctx context.Context, userID int64, name string) (debts.Debt, error)
	Pay(ctx context.Context, in debts.PaymentInput) (debts.Debt, error)
	Plan(ctx context.Context, userID int64, extra decimal.Decimal, today time.Time) (debts.Schedule, debts.Schedule, error)
	NetWorth(ctx context.Context, userID int64, today time.Time) (debts.Snapshot, error)
}

// noAdviceRule goes into every prompt: the service counts, it does not advise.
const noAdviceRule = `
19. Ты ведёшь учёт и считаешь, но не даёшь советов, что делать с деньгами. Никогда не говори, что купить, продать, куда вложить, когда входить или выходить, брать ли кредит, какой банк, бумагу или монету выбрать. Не называй конкретные активы как подходящие.
    На такие вопросы ответь, что это решение самого пользователя и что ты помогаешь с учётом, объясни понятие общими словами, если просят, и предложи посчитать его цифры. Бот не является финансовым консультантом.`

// debtRules explain debts, after the rules of accounts and tasks.
const debtRules = `
20. Долги: «у меня кредитка 150000 под 29,9, минимальный 4500» — add_debt. «Заплатил по кредитке 5000» — add_debt_payment, а не add_expense: трату запишет сам код. Если есть регулярный платёж по этому долгу, передай regular_payment_id.
    «Сколько я должен», «какой чистый капитал», «как быстрее закрыть долги», «что будет, если платить на 5000 больше» — get_debts, с extra, если названа сумма сверх минимальных платежей. Цифры пересказывай из результата, сам не считай. Лавина и снежный ком — способы подсчёта, не выбирай за пользователя.`

func debtToolDefs(today time.Time) []llm.Tool {
	d := func(t time.Time) string { return t.Format(time.DateOnly) }
	return []llm.Tool{
		{
			Name:        toolAddDebt,
			Description: "Добавить долг пользователя: кредит, кредитку, рассрочку, микрозайм, долг знакомому.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "name": {"type": "string", "description": "Название, как сказал пользователь"},
    "kind": {"type": "string", "enum": ["card", "loan", "installment", "microloan", "friend"], "description": "card — кредитка, loan — кредит, installment — рассрочка, microloan — микрозайм, friend — долг знакомому"},
    "balance": {"type": "number", "description": "Сколько осталось отдать, в рублях"},
    "rate": {"type": "number", "description": "Ставка в процентах годовых, 0 без процентов"},
    "min_payment": {"type": "number", "description": "Минимальный платёж в месяц"},
    "due_day": {"type": "integer", "description": "День месяца платежа, если назван"}
  },
  "required": ["name", "balance"]
}`),
			Examples: []llm.ToolExample{
				{Request: "кредитка 150000 под 29,9, минимальный платёж 4500 10 числа", Params: map[string]any{"name": "кредитка", "kind": "card", "balance": 150000, "rate": 29.9, "min_payment": 4500, "due_day": 10}},
			},
		},
		{
			Name:        toolPayDebt,
			Description: "Записать платёж по долгу. Трату код запишет сам: add_expense не вызывай.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "debt": {"type": "string", "description": "Название долга из списка"},
    "amount": {"type": "number", "description": "Сумма в рублях"},
    "date": {"type": "string", "description": "Дата YYYY-MM-DD"},
    "regular_payment_id": {"type": "integer", "description": "id регулярного платежа, если это он"}
  },
  "required": ["debt", "amount", "date"]
}`),
			Examples: []llm.ToolExample{
				{Request: "заплатил по кредитке 5000", Params: map[string]any{"debt": "кредитка", "amount": 5000, "date": d(today)}},
			},
		},
		{
			Name: toolGetDebts,
			Description: "Показать долги, чистый капитал (счета минус долги) и план погашения двумя способами: " +
				"лавина и снежный ком, сколько месяцев и сколько переплаты.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "extra": {"type": "number", "description": "Сумма в месяц сверх минимальных платежей, если названа"}
  }
}`),
			Examples: []llm.ToolExample{
				{Request: "сколько я должен", Params: map[string]any{}},
				{Request: "что будет, если платить на 5000 больше", Params: map[string]any{"extra": 5000}},
			},
		},
	}
}

func (a *Agent) addDebt(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	var args struct {
		Name       string      `json:"name"`
		Kind       string      `json:"kind"`
		Balance    json.Number `json:"balance"`
		Rate       json.Number `json:"rate"`
		MinPayment json.Number `json:"min_payment"`
		DueDay     json.Number `json:"due_day"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	balance, err := parseAmount(args.Balance, true)
	if err != nil {
		return nil, err
	}
	d := debts.Debt{UserID: t.userID, Name: args.Name, Kind: args.Kind, Principal: *balance, Rate: decimal.Zero, MinPayment: decimal.Zero}
	if args.Rate != "" {
		if d.Rate, err = decimal.NewFromString(args.Rate.String()); err != nil {
			return nil, badArgs("rate must be a number")
		}
	}
	if args.MinPayment != "" {
		if d.MinPayment, err = decimal.NewFromString(args.MinPayment.String()); err != nil {
			return nil, badArgs("min_payment must be a number")
		}
	}
	if args.DueDay != "" {
		day, err := args.DueDay.Int64()
		if err != nil {
			return nil, badArgs("due_day must be an integer")
		}
		dueDay := int(day)
		d.DueDay = &dueDay
	}
	saved, err := a.debts.Create(ctx, d, t.today)
	if err != nil {
		return nil, err
	}
	return map[string]any{"added": debtItem(saved)}, nil
}

func (a *Agent) payDebt(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	var args struct {
		Debt             string      `json:"debt"`
		Amount           json.Number `json:"amount"`
		Date             string      `json:"date"`
		RegularPaymentID json.Number `json:"regular_payment_id"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	d, err := a.debts.ByName(ctx, t.userID, args.Debt)
	if errors.Is(err, debts.ErrNotFound) {
		list, _, listErr := a.debts.List(ctx, t.userID)
		if listErr != nil {
			return nil, listErr
		}
		names := make([]string, len(list))
		for i, x := range list {
			names[i] = x.Name
		}
		return nil, badArgs("no debt %q; the user's debts: %s. Ask the user which one", args.Debt, strings.Join(names, ", "))
	}
	if err != nil {
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
	in := debts.PaymentInput{UserID: t.userID, DebtID: d.ID, Amount: *amount, Date: date, Today: t.today, Now: time.Now()}
	if in.RegularPaymentID, err = a.checkRegularPayment(ctx, t, args.RegularPaymentID, "expense"); err != nil {
		return nil, err
	}
	paid, err := a.debts.Pay(ctx, in)
	if err != nil {
		return nil, err
	}
	return map[string]any{"paid": money(*amount), "debt": debtItem(paid), "closed": paid.ClosedAt != nil}, nil
}

func (a *Agent) getDebts(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	var args struct {
		Extra json.Number `json:"extra"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	extra := decimal.Zero
	if args.Extra != "" {
		var err error
		if extra, err = decimal.NewFromString(args.Extra.String()); err != nil {
			return nil, badArgs("extra must be a number")
		}
	}
	list, total, err := a.debts.List(ctx, t.userID)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, len(list))
	for i, d := range list {
		items[i] = debtItem(d)
	}
	worth, err := a.debts.NetWorth(ctx, t.userID, t.today)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"debts": items, "total": money(total),
		"net_worth": money(worth.Net()), "assets": money(worth.Assets),
	}
	if len(list) == 0 {
		return out, nil
	}
	avalanche, snowball, err := a.debts.Plan(ctx, t.userID, extra, t.today)
	if errors.Is(err, debts.ErrNeverPaid) {
		out["plan"] = "with these payments the debts are never paid off: they do not cover the interest"
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out["avalanche"] = scheduleItem(avalanche)
	out["snowball"] = scheduleItem(snowball)
	out["interest_saved_by_avalanche"] = money(snowball.Interest.Sub(avalanche.Interest))
	return out, nil
}

func debtItem(d debts.Debt) map[string]any {
	return map[string]any{"name": d.Name, "balance": money(d.Balance), "rate": d.Rate.String(), "min_payment": money(d.MinPayment)}
}

func scheduleItem(s debts.Schedule) map[string]any {
	return map[string]any{"months": s.Months, "interest": money(s.Interest)}
}
