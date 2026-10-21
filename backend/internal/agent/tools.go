package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
)

// Tool names exposed to the model.
const (
	toolAdd     = "add_expense"
	toolFind    = "find_expenses"
	toolSummary = "get_spending_summary"
	toolUpdate  = "update_expense"
	toolDelete  = "delete_expense"
)

const (
	defaultFindLimit = 10
	maxFindLimit     = 30
)

// categorySchema lists category codes; the prompt explains what goes where.
const categorySchema = `{"type": "string", "enum": ["food", "transport", "housing", "entertainment", "health", "shopping", "other"],
  "description": "food — еда, продукты, кафе, кофе; transport — такси, метро, бензин; housing — аренда, ЖКХ, ремонт; entertainment — кино, игры, подписки; health — аптека, врачи, спорт; shopping — одежда, техника, подарки; other — всё остальное"}`

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
			Name:        toolAdd,
			Description: "Добавить трату пользователя.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "amount": {"type": "number", "description": "Сумма в рублях, больше нуля. «5к» = 5000"},
    "category": ` + categorySchema + `,
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
			},
		},
		{
			Name: toolFind,
			Description: "Найти траты пользователя: список с id и итогом. Используй, чтобы показать траты, " +
				"и чтобы узнать id перед изменением или удалением.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "date_from": {"type": "string", "description": "Начало периода YYYY-MM-DD включительно"},
    "date_to": {"type": "string", "description": "Конец периода YYYY-MM-DD включительно"},
    "category": ` + categorySchema + `,
    "text": {"type": "string", "description": "Слово из комментария траты, например «такси»"},
    "last_added": {"type": "boolean", "description": "true — сортировать по времени добавления, самые последние первыми («последняя трата»)"},
    "limit": {"type": "integer", "description": "Сколько трат вернуть, по умолчанию 10, максимум 30"}
  }
}`),
			Examples: []llm.ToolExample{
				{Request: "покажи траты за неделю", Params: map[string]any{"date_from": d(weekStart), "date_to": d(today)}},
				{Request: "последнее было не 350, а 450", Params: map[string]any{"last_added": true, "limit": 1}},
				{Request: "удали такси", Params: map[string]any{"text": "такси"}},
			},
		},
		{
			Name:        toolSummary,
			Description: "Посчитать, сколько пользователь потратил за период: общая сумма и разбивка по категориям.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "date_from": {"type": "string", "description": "Начало периода YYYY-MM-DD включительно"},
    "date_to": {"type": "string", "description": "Конец периода YYYY-MM-DD включительно"},
    "category": ` + categorySchema + `
  }
}`),
			Examples: []llm.ToolExample{
				{Request: "сколько потратил на еду в этом месяце", Params: map[string]any{"date_from": d(monthStart), "date_to": d(today), "category": "food"}},
			},
		},
		{
			Name:        toolUpdate,
			Description: "Изменить трату по id. Передай только поля, которые нужно поменять. id бери из find_expenses.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "id": {"type": "integer", "description": "id траты"},
    "amount": {"type": "number", "description": "Новая сумма в рублях"},
    "category": ` + categorySchema + `,
    "date": {"type": "string", "description": "Новая дата YYYY-MM-DD"},
    "comment": {"type": "string", "description": "Новый комментарий"}
  },
  "required": ["id"]
}`),
			Examples: []llm.ToolExample{
				{Request: "последнее было не 350, а 450 (id последней траты 17)", Params: map[string]any{"id": 17, "amount": 450}},
			},
		},
		{
			Name:        toolDelete,
			Description: "Удалить трату по id. id бери из find_expenses.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {"id": {"type": "integer", "description": "id траты"}},
  "required": ["id"]
}`),
			Examples: []llm.ToolExample{
				{Request: "удали такси (find_expenses нашёл одну трату с id 23)", Params: map[string]any{"id": 23}},
			},
		},
	}
}

// lastWeekday returns the most recent day with the given weekday, today included.
func lastWeekday(today time.Time, wd time.Weekday) time.Time {
	return today.AddDate(0, 0, -((int(today.Weekday()) - int(wd) + 7) % 7))
}

// Expenses is the part of ledger.Service the agent needs.
type Expenses interface {
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

// turn is what the tools know about the request: whose expenses they work
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
	case toolAdd:
		result, err = a.addExpense(ctx, t, call.Arguments)
	case toolFind:
		result, err = a.findExpenses(ctx, t, call.Arguments)
	case toolSummary:
		result, err = a.summary(ctx, t, call.Arguments)
	case toolUpdate:
		result, err = a.updateExpense(ctx, t, call.Arguments)
	case toolDelete:
		result, err = a.deleteExpense(ctx, t, call.Arguments)
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
		if verr.Field == "spent_at" {
			msg += ". A weekday without details means the most recent past one: take its date from the list of the last 7 days. " +
				"If the user really meant a future date, tell them only past expenses are recorded."
		}
		result = map[string]string{"error": msg}
	case errors.Is(err, ledger.ErrNotFound):
		result = map[string]string{"error": "expense not found; call find_expenses to get a valid id"}
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

func (a *Agent) addExpense(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
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
	date, err := parseDate("date", args.Date)
	if err != nil {
		return nil, err
	}
	e, err := a.expenses.Create(ctx, ledger.CreateInput{
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
	Category  string `json:"category"`
	Text      string `json:"text"`
	LastAdded bool   `json:"last_added"`
	Limit     int    `json:"limit"`
}

func (a *Agent) findExpenses(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	scope := t.scope()
	var args findArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	f, err := periodFilter(args.DateFrom, args.DateTo, args.Category)
	if err != nil {
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
	page, err := a.expenses.List(ctx, scope, f)
	if err != nil {
		return nil, err
	}
	items := make([]item, len(page.Items))
	for i, e := range page.Items {
		items[i] = t.item(e)
	}
	return map[string]any{
		"expenses":    items,
		"total_count": page.Total,
		"total_sum":   money(page.Sum),
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
	s, err := a.expenses.Summary(ctx, scope, f)
	if err != nil {
		return nil, err
	}
	type row struct {
		Category string      `json:"category"`
		Sum      json.Number `json:"sum"`
		Count    int         `json:"count"`
	}
	rows := make([]row, len(s.Items))
	for i, t := range s.Items {
		rows[i] = row{Category: t.Category.Label(), Sum: money(t.Sum), Count: t.Count}
	}
	return map[string]any{"total_sum": money(s.Sum), "count": s.Count, "by_category": rows}, nil
}

type updateArgs struct {
	ID       json.Number `json:"id"`
	Amount   json.Number `json:"amount"`
	Category string      `json:"category"`
	Date     string      `json:"date"`
	Comment  *string     `json:"comment"`
}

func (a *Agent) updateExpense(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
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

	before, err := a.expenses.Get(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	after, err := a.expenses.Update(ctx, scope, id, p)
	if err != nil {
		return nil, err
	}
	return map[string]any{"before": t.item(before), "after": t.item(after)}, nil
}

type deleteArgs struct {
	ID json.Number `json:"id"`
}

func (a *Agent) deleteExpense(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	scope := t.scope()
	var args deleteArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	id, err := parseID(args.ID)
	if err != nil {
		return nil, err
	}
	e, err := a.expenses.Get(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	if err := a.expenses.Delete(ctx, scope, id); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": t.item(e)}, nil
}

// item is the compact representation of an expense shown to the model.
type item struct {
	ID       int64       `json:"id"`
	Amount   json.Number `json:"amount"`
	Category string      `json:"category"`
	Date     string      `json:"date"`
	// DateText is the date as the reply should say it ("вчера", "16 октября"),
	// so the model does not format dates itself.
	DateText string `json:"date_text"`
	Comment  string `json:"comment,omitempty"`
}

func (t turn) item(e ledger.Operation) item {
	return item{
		ID:       e.ID,
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
		return 0, badArgs("id must be a positive integer from find_expenses")
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
	for _, c := range ledger.Categories {
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
		return nil, badArgs("unknown category %q; use one of: food, transport, housing, entertainment, health, shopping, other", s)
	}
	return &c, nil
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
