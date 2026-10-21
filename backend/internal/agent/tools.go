package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
)

// Tool names exposed to the model.
const (
	toolAddExpense = "add_expense"
	toolAddIncome  = "add_income"
	toolFind       = "find_operations"
	toolSummary    = "get_summary"
	toolUpdate     = "update_operation"
	toolDelete     = "delete_operation"
)

const (
	defaultFindLimit = 10
	maxFindLimit     = 30
)

// Category schemas list category codes; the prompt explains what goes where.
const (
	expenseCategoryDesc = "food — еда, продукты, кафе, кофе; transport — такси, метро, бензин; housing — аренда, ЖКХ, ремонт; " +
		"entertainment — кино, игры, подписки; health — аптека, врачи, спорт; shopping — одежда, техника, подарки другим; other — всё остальное"
	incomeCategoryDesc = "salary — зарплата, аванс, премия; freelance — подработка, заказ, фриланс; gift — подарили деньги; " +
		"interest — проценты по вкладу, кэшбэк; refund — вернули деньги за покупку или вернули долг; other_income — другой доход"

	expenseCategorySchema = `{"type": "string", "enum": ["food", "transport", "housing", "entertainment", "health", "shopping", "other"],
  "description": "` + expenseCategoryDesc + `"}`
	incomeCategorySchema = `{"type": "string", "enum": ["salary", "freelance", "gift", "interest", "refund", "other_income"],
  "description": "` + incomeCategoryDesc + `"}`
	anyCategorySchema = `{"type": "string",
  "enum": ["food", "transport", "housing", "entertainment", "health", "shopping", "other", "salary", "freelance", "gift", "interest", "refund", "other_income"],
  "description": "Траты: ` + expenseCategoryDesc + `. Доходы: ` + incomeCategoryDesc + `"}`
	kindSchema = `{"type": "string", "enum": ["expense", "income"], "description": "expense — только траты, income — только доходы; не указывай, если нужны и те и другие"}`
)

// toolDefs returns the functions the model may call. Note that none of them
// has a user parameter: the owner always comes from the Telegram update.
//
// Few-shot examples carry real dates relative to today: with placeholders like
// "<yesterday>" the model tends to drop the date and record the expense for today.
func toolDefs(today time.Time) []llm.Tool {
	d := func(t time.Time) string { return t.Format(time.DateOnly) }
	weekStart := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	monthStart := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastMonday := lastWeekday(today, time.Monday)
	lastSaturday := lastWeekday(today, time.Saturday)

	return []llm.Tool{
		{
			Name:        toolAddExpense,
			Description: "Добавить трату пользователя: деньги, которые он потратил.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "amount": {"type": "number", "description": "Сумма в рублях, больше нуля. «5к» = 5000"},
    "category": ` + expenseCategorySchema + `,
    "date": {"type": "string", "description": "Дата траты YYYY-MM-DD. Если пользователь не назвал день — сегодняшняя дата. «Вчера», «в понедельник», «3 числа» — возьми из календаря в инструкциях"},
    "comment": {"type": "string", "description": "На что потрачено, коротко, как сказал пользователь: «кофе», «такси до работы»"}
  },
  "required": ["amount", "category", "comment", "date"]
}`),
			Examples: []llm.ToolExample{
				{Request: "кофе 350", Params: map[string]any{"amount": 350, "category": "food", "comment": "кофе", "date": d(today)}},
				{Request: "такси 1200 вчера", Params: map[string]any{"amount": 1200, "category": "transport", "comment": "такси", "date": d(today.AddDate(0, 0, -1))}},
				{Request: "бензин 3000 в понедельник", Params: map[string]any{"amount": 3000, "category": "transport", "comment": "бензин", "date": d(lastMonday)}},
				{Request: "продукты 2.5к в субботу", Params: map[string]any{"amount": 2500, "category": "food", "comment": "продукты", "date": d(lastSaturday)}},
				{Request: "обед 450 3 числа", Params: map[string]any{"amount": 450, "category": "food", "comment": "обед", "date": d(time.Date(today.Year(), today.Month(), 3, 0, 0, 0, 0, time.UTC))}},
				{Request: "перевёл маме 2000", Params: map[string]any{"amount": 2000, "category": "other", "comment": "перевёл маме", "date": d(today)}},
			},
		},
		{
			Name:        toolAddIncome,
			Description: "Добавить доход пользователя: деньги, которые он получил.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "amount": {"type": "number", "description": "Сумма в рублях, больше нуля. «80к» = 80000"},
    "category": ` + incomeCategorySchema + `,
    "date": {"type": "string", "description": "Дата получения YYYY-MM-DD. Если пользователь не назвал день — сегодняшняя дата"},
    "comment": {"type": "string", "description": "Откуда деньги, коротко, как сказал пользователь: «зарплата», «заказ на логотип»"}
  },
  "required": ["amount", "category", "comment", "date"]
}`),
			Examples: []llm.ToolExample{
				{Request: "зарплата 80000", Params: map[string]any{"amount": 80000, "category": "salary", "comment": "зарплата", "date": d(today)}},
				{Request: "пришла зп 45к вчера", Params: map[string]any{"amount": 45000, "category": "salary", "comment": "зарплата", "date": d(today.AddDate(0, 0, -1))}},
				{Request: "вернули долг 2000", Params: map[string]any{"amount": 2000, "category": "refund", "comment": "вернули долг", "date": d(today)}},
				{Request: "кэшбэк 350", Params: map[string]any{"amount": 350, "category": "interest", "comment": "кэшбэк", "date": d(today)}},
			},
		},
		{
			Name: toolFind,
			Description: "Найти траты и доходы пользователя: список с id и итогами. Используй, чтобы показать операции, " +
				"и чтобы узнать id перед изменением или удалением.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "date_from": {"type": "string", "description": "Начало периода YYYY-MM-DD включительно"},
    "date_to": {"type": "string", "description": "Конец периода YYYY-MM-DD включительно"},
    "kind": ` + kindSchema + `,
    "category": ` + anyCategorySchema + `,
    "text": {"type": "string", "description": "Одно слово из комментария, без суммы и даты, например «такси»"},
    "last_added": {"type": "boolean", "description": "true — сортировать по времени добавления, самые последние первыми («последняя трата», «последнее»)"},
    "limit": {"type": "integer", "description": "Сколько операций вернуть, по умолчанию 10, максимум 30"}
  }
}`),
			Examples: []llm.ToolExample{
				{Request: "покажи траты за неделю", Params: map[string]any{"date_from": d(weekStart), "date_to": d(today), "kind": "expense"}},
				{Request: "какие были доходы в этом месяце", Params: map[string]any{"date_from": d(monthStart), "date_to": d(today), "kind": "income"}},
				{Request: "последнее было не 350, а 450", Params: map[string]any{"last_added": true, "limit": 1}},
				{Request: "удали такси", Params: map[string]any{"text": "такси"}},
				{Request: "долг 2000 — это не трата, мне вернули долг", Params: map[string]any{"text": "долг"}},
			},
		},
		{
			Name: toolSummary,
			Description: "Посчитать за период, сколько пользователь потратил и получил: суммы трат и доходов, разбивка по категориям " +
				"и баланс (доходы минус траты — сколько осталось). category передавай, только если пользователь сам назвал " +
				"категорию («на еду», «зарплаты»): «сколько получил» — это все доходы, без категории.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "date_from": {"type": "string", "description": "Начало периода YYYY-MM-DD включительно"},
    "date_to": {"type": "string", "description": "Конец периода YYYY-MM-DD включительно"},
    "category": ` + anyCategorySchema + `
  }
}`),
			Examples: []llm.ToolExample{
				{Request: "сколько потратил на еду в этом месяце", Params: map[string]any{"date_from": d(monthStart), "date_to": d(today), "category": "food"}},
				{Request: "сколько осталось в этом месяце", Params: map[string]any{"date_from": d(monthStart), "date_to": d(today)}},
				{Request: "сколько я получил в этом месяце", Params: map[string]any{"date_from": d(monthStart), "date_to": d(today)}},
			},
		},
		{
			Name: toolUpdate,
			Description: "Изменить трату или доход по id. Передай только поля, которые нужно поменять. id бери из find_operations. " +
				"Если трата на самом деле доход (или наоборот), поменяй категорию на категорию другого вида.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "id": {"type": "integer", "description": "id операции"},
    "amount": {"type": "number", "description": "Новая сумма в рублях"},
    "category": ` + anyCategorySchema + `,
    "date": {"type": "string", "description": "Новая дата YYYY-MM-DD"},
    "comment": {"type": "string", "description": "Новый комментарий"}
  },
  "required": ["id"]
}`),
			Examples: []llm.ToolExample{
				{Request: "последнее было не 350, а 450 (id последней операции 17)", Params: map[string]any{"id": 17, "amount": 450}},
				{Request: "это был не расход, а возврат долга (id 21)", Params: map[string]any{"id": 21, "category": "refund"}},
			},
		},
		{
			Name:        toolDelete,
			Description: "Удалить трату или доход по id. id бери из find_operations.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {"id": {"type": "integer", "description": "id операции"}},
  "required": ["id"]
}`),
			Examples: []llm.ToolExample{
				{Request: "удали такси (find_operations нашёл одну трату с id 23)", Params: map[string]any{"id": 23}},
			},
		},
	}
}

// lastWeekday returns the most recent day with the given weekday, today included.
func lastWeekday(today time.Time, wd time.Weekday) time.Time {
	return today.AddDate(0, 0, -((int(today.Weekday()) - int(wd) + 7) % 7))
}

// Ledger is the part of ledger.Service the agent needs.
type Ledger interface {
	Create(ctx context.Context, in ledger.CreateInput) (ledger.Operation, error)
	Get(ctx context.Context, s ledger.Scope, id int64) (ledger.Operation, error)
	Update(ctx context.Context, s ledger.Scope, id int64, p ledger.Patch) (ledger.Operation, error)
	Delete(ctx context.Context, s ledger.Scope, id int64) error
	List(ctx context.Context, s ledger.Scope, f ledger.Filter) (ledger.Page, error)
	Summary(ctx context.Context, s ledger.Scope, f ledger.Filter) (ledger.Summary, error)
}

// toolError is an error the model can fix: it is sent back as the tool result.
type toolError struct{ msg string }

func (e *toolError) Error() string { return e.msg }

func badArgs(format string, args ...any) error { return &toolError{msg: fmt.Sprintf(format, args...)} }

// turn is what the tools know about the request: whose operations they work
// with and what date it is for that user.
type turn struct {
	userID int64 // from Telegram, never from the model
	today  time.Time
}

func (t turn) scope() ledger.Scope { return ledger.OwnedBy(t.userID) }

// runTool executes a tool call for the turn's user and returns the JSON result
// for the model. Errors the model can react to are encoded into the result;
// only infrastructure failures are returned as error.
func (a *Agent) runTool(ctx context.Context, t turn, call *llm.ToolCall) (string, error) {
	var (
		result any
		err    error
	)
	switch call.Name {
	case toolAddExpense:
		result, err = a.add(ctx, t, ledger.KindExpense, call.Arguments)
	case toolAddIncome:
		result, err = a.add(ctx, t, ledger.KindIncome, call.Arguments)
	case toolFind:
		result, err = a.find(ctx, t, call.Arguments)
	case toolSummary:
		result, err = a.summary(ctx, t, call.Arguments)
	case toolUpdate:
		result, err = a.update(ctx, t, call.Arguments)
	case toolDelete:
		result, err = a.delete(ctx, t, call.Arguments)
	default:
		err = badArgs("unknown function %q", call.Name)
	}

	var (
		terr *toolError
		verr *ledger.ValidationError
	)
	switch {
	case err == nil:
	case errors.As(err, &terr):
		result = map[string]string{"error": terr.msg}
	case errors.As(err, &verr):
		msg := "invalid " + verr.Field + ": " + verr.Message
		if verr.Field == "date" {
			msg += ". A weekday without details means the most recent past one: take its date from the list of the last 7 days. " +
				"If the user really meant a future date, tell them only past operations are recorded."
		}
		result = map[string]string{"error": msg}
	case errors.Is(err, ledger.ErrNotFound):
		result = map[string]string{"error": "operation not found; call find_operations to get a valid id"}
	default:
		return "", err
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode tool result: %w", err)
	}
	return string(out), nil
}

type addArgs struct {
	Amount   json.Number `json:"amount"`
	Category string      `json:"category"`
	Date     string      `json:"date"`
	Comment  string      `json:"comment"`
}

// add records an expense or an income: the tool decides the kind, and a
// category of the other kind is sent back to the model as a mistake.
func (a *Agent) add(ctx context.Context, t turn, kind ledger.Kind, raw json.RawMessage) (any, error) {
	var args addArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	amount, err := parseAmount(args.Amount, true)
	if err != nil {
		return nil, err
	}
	category, err := parseCategory(args.Category, true)
	if err != nil {
		return nil, err
	}
	if category.Kind() != kind {
		if kind == ledger.KindIncome {
			return nil, badArgs("%s is an expense category: an income needs one of salary, freelance, gift, interest, refund, other_income, "+
				"or call add_expense if the user spent money", *category)
		}
		return nil, badArgs("%s is an income category: an expense needs one of food, transport, housing, entertainment, health, shopping, other, "+
			"or call add_income if the user received money", *category)
	}
	date, err := parseDate("date", args.Date)
	if err != nil {
		return nil, err
	}
	e, err := a.ledger.Create(ctx, ledger.CreateInput{
		UserID:   t.userID,
		Amount:   *amount,
		Category: *category,
		Date:     date,
		Comment:  args.Comment,
		Today:    t.today,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"added": t.item(e)}, nil
}

type findArgs struct {
	DateFrom  string `json:"date_from"`
	DateTo    string `json:"date_to"`
	Kind      string `json:"kind"`
	Category  string `json:"category"`
	Text      string `json:"text"`
	LastAdded bool   `json:"last_added"`
	Limit     int    `json:"limit"`
}

func (a *Agent) find(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	scope := t.scope()
	var args findArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	f, err := periodFilter(args.DateFrom, args.DateTo, args.Category)
	if err != nil {
		return nil, err
	}
	if f.Kind, err = parseKind(args.Kind); err != nil {
		return nil, err
	}
	f.Query = args.Text
	f.Limit = min(max(args.Limit, 0), maxFindLimit)
	if f.Limit == 0 {
		f.Limit = defaultFindLimit
	}
	if args.LastAdded {
		f.Sort = ledger.SortByCreatedAt
	}
	page, err := a.ledger.List(ctx, scope, f)
	if err != nil {
		return nil, err
	}
	items := make([]item, len(page.Items))
	for i, e := range page.Items {
		items[i] = t.item(e)
	}
	return map[string]any{
		"operations":  items,
		"total_count": page.Total,
		"spent":       money(page.ExpenseSum),
		"received":    money(page.IncomeSum),
	}, nil
}

type summaryArgs struct {
	DateFrom string `json:"date_from"`
	DateTo   string `json:"date_to"`
	Category string `json:"category"`
}

func (a *Agent) summary(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	scope := t.scope()
	var args summaryArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	f, err := periodFilter(args.DateFrom, args.DateTo, args.Category)
	if err != nil {
		return nil, err
	}
	s, err := a.ledger.Summary(ctx, scope, f)
	if err != nil {
		return nil, err
	}
	type row struct {
		Category string      `json:"category"`
		Sum      json.Number `json:"sum"`
		Count    int         `json:"count"`
	}
	byCategory := func(k ledger.Kind) []row {
		items := s.Of(k)
		rows := make([]row, len(items))
		for i, c := range items {
			rows[i] = row{Category: c.Category.Label(), Sum: money(c.Sum), Count: c.Count}
		}
		return rows
	}
	// Totals come first and flat (a struct keeps the order, a map would sort
	// the keys): with nested objects the model tends to quote one category
	// instead of the total.
	return struct {
		SpentTotal         json.Number `json:"spent_total"`
		ReceivedTotal      json.Number `json:"received_total"`
		Balance            json.Number `json:"balance"`
		SpentByCategory    []row       `json:"spent_by_category"`
		ReceivedByCategory []row       `json:"received_by_category"`
	}{
		money(s.Expense.Sum), money(s.Income.Sum), money(s.Balance()),
		byCategory(ledger.KindExpense), byCategory(ledger.KindIncome),
	}, nil
}

type updateArgs struct {
	ID       json.Number `json:"id"`
	Amount   json.Number `json:"amount"`
	Category string      `json:"category"`
	Date     string      `json:"date"`
	Comment  *string     `json:"comment"`
}

func (a *Agent) update(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	scope := t.scope()
	var args updateArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	id, err := parseID(args.ID)
	if err != nil {
		return nil, err
	}
	var p ledger.Patch
	if p.Amount, err = parseAmount(args.Amount, false); err != nil {
		return nil, err
	}
	if p.Category, err = parseCategory(args.Category, false); err != nil {
		return nil, err
	}
	if p.Date, err = parseDate("date", args.Date); err != nil {
		return nil, err
	}
	p.Comment = args.Comment
	p.Today = t.today
	if p.Amount == nil && p.Category == nil && p.Date == nil && p.Comment == nil {
		return nil, badArgs("nothing to update: pass at least one of amount, category, date, comment")
	}

	before, err := a.ledger.Get(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	after, err := a.ledger.Update(ctx, scope, id, p)
	if err != nil {
		return nil, err
	}
	return map[string]any{"before": t.item(before), "after": t.item(after)}, nil
}

type deleteArgs struct {
	ID json.Number `json:"id"`
}

func (a *Agent) delete(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	scope := t.scope()
	var args deleteArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	id, err := parseID(args.ID)
	if err != nil {
		return nil, err
	}
	e, err := a.ledger.Get(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	if err := a.ledger.Delete(ctx, scope, id); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": t.item(e)}, nil
}

// item is the compact representation of an operation shown to the model.
type item struct {
	ID       int64       `json:"id"`
	Kind     string      `json:"kind"` // "расход" or "доход"
	Amount   json.Number `json:"amount"`
	Category string      `json:"category"`
	Date     string      `json:"date"`
	// DateText is the date as the reply should say it ("вчера", "16 октября"),
	// so the model does not format dates itself.
	DateText string `json:"date_text"`
	Comment  string `json:"comment,omitempty"`
}

func (t turn) item(e ledger.Operation) item {
	kind := "расход"
	if e.Kind() == ledger.KindIncome {
		kind = "доход"
	}
	return item{
		ID:       e.ID,
		Kind:     kind,
		Amount:   money(e.Amount),
		Category: e.Category.Label(),
		Date:     e.Date.Format(time.DateOnly),
		DateText: humanDate(e.Date, t.today),
		Comment:  e.Comment,
	}
}

var monthsGenitive = [...]string{"", "января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря"}

// humanDate formats a date the way people say it in Russian.
func humanDate(d, today time.Time) string {
	switch {
	case d.Equal(today):
		return "сегодня"
	case d.Equal(today.AddDate(0, 0, -1)):
		return "вчера"
	case d.Equal(today.AddDate(0, 0, -2)):
		return "позавчера"
	case d.Year() == today.Year():
		return fmt.Sprintf("%d %s", d.Day(), monthsGenitive[d.Month()])
	default:
		return fmt.Sprintf("%d %s %d", d.Day(), monthsGenitive[d.Month()], d.Year())
	}
}

func money(d decimal.Decimal) json.Number { return json.Number(d.StringFixedBank(2)) }

func decodeArgs(raw json.RawMessage, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return badArgs("invalid arguments: %v", err)
	}
	return nil
}

func parseID(n json.Number) (int64, error) {
	id, err := n.Int64()
	if err != nil || id <= 0 {
		return 0, badArgs("id must be a positive integer from find_operations")
	}
	return id, nil
}

func parseAmount(n json.Number, required bool) (*decimal.Decimal, error) {
	if n == "" {
		if required {
			return nil, badArgs("amount is required")
		}
		return nil, nil
	}
	d, err := decimal.NewFromString(n.String())
	if err != nil {
		return nil, badArgs("amount must be a number in rubles, got %q", n)
	}
	return &d, nil
}

// categoryAliases accept Russian names too: models sometimes ignore the enum.
var categoryAliases = func() map[string]ledger.Category {
	m := map[string]ledger.Category{}
	for _, c := range append(slices.Clone(ledger.ExpenseCategories), ledger.IncomeCategories...) {
		m[string(c)] = c
		m[c.Label()] = c
		m[strings.ReplaceAll(c.Label(), "ё", "е")] = c
	}
	return m
}()

func parseCategory(s string, required bool) (*ledger.Category, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		if required {
			return nil, badArgs("category is required")
		}
		return nil, nil
	}
	c, ok := categoryAliases[s]
	if !ok {
		return nil, badArgs("unknown category %q; expenses: food, transport, housing, entertainment, health, shopping, other; "+
			"incomes: salary, freelance, gift, interest, refund, other_income", s)
	}
	return &c, nil
}

func parseKind(s string) (*ledger.Kind, error) {
	k := ledger.Kind(strings.ToLower(strings.TrimSpace(s)))
	if k == "" {
		return nil, nil
	}
	if !k.Valid() {
		return nil, badArgs("kind must be expense or income, got %q", s)
	}
	return &k, nil
}

func parseDate(field, s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return nil, badArgs("%s must be in YYYY-MM-DD format, got %q; compute it from today's date in the instructions", field, s)
	}
	return &t, nil
}

func periodFilter(from, to, category string) (ledger.Filter, error) {
	var (
		f   ledger.Filter
		err error
	)
	if f.From, err = parseDate("date_from", from); err != nil {
		return f, err
	}
	if f.To, err = parseDate("date_to", to); err != nil {
		return f, err
	}
	if f.Category, err = parseCategory(category, false); err != nil {
		return f, err
	}
	return f, nil
}
