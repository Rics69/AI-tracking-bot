package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/expense"
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

// tools are the functions the model may call. Note that none of them has a
// user parameter: the owner always comes from the Telegram update.
var tools = []llm.Tool{
	{
		Name:        toolAdd,
		Description: "Добавить трату пользователя.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "amount": {"type": "number", "description": "Сумма в рублях, больше нуля. «5к» = 5000"},
    "category": ` + categorySchema + `,
    "date": {"type": "string", "description": "Дата траты YYYY-MM-DD. Не указывай, если пользователь не назвал дату — будет сегодня"},
    "comment": {"type": "string", "description": "На что потрачено, коротко, как сказал пользователь: «кофе», «такси до работы»"}
  },
  "required": ["amount", "category", "comment"]
}`),
		Examples: []llm.ToolExample{
			{Request: "кофе 350", Params: map[string]any{"amount": 350, "category": "food", "comment": "кофе"}},
			{Request: "такси 1200 вчера", Params: map[string]any{"amount": 1200, "category": "transport", "comment": "такси", "date": "<вчерашняя дата>"}},
			{Request: "кроссовки 7.5к", Params: map[string]any{"amount": 7500, "category": "shopping", "comment": "кроссовки"}},
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
			{Request: "покажи траты за неделю", Params: map[string]any{"date_from": "<понедельник этой недели>", "date_to": "<сегодня>"}},
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
			{Request: "сколько потратил на еду в этом месяце", Params: map[string]any{"date_from": "<первое число месяца>", "date_to": "<сегодня>", "category": "food"}},
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

// Expenses is the part of expense.Service the agent needs.
type Expenses interface {
	Create(ctx context.Context, in expense.CreateInput) (expense.Expense, error)
	Get(ctx context.Context, s expense.Scope, id int64) (expense.Expense, error)
	Update(ctx context.Context, s expense.Scope, id int64, p expense.Patch) (expense.Expense, error)
	Delete(ctx context.Context, s expense.Scope, id int64) error
	List(ctx context.Context, s expense.Scope, f expense.Filter) (expense.Page, error)
	Summary(ctx context.Context, s expense.Scope, f expense.Filter) (expense.Summary, error)
}

// toolError is an error the model can fix: it is sent back as the tool result.
type toolError struct{ msg string }

func (e *toolError) Error() string { return e.msg }

func badArgs(format string, args ...any) error { return &toolError{msg: fmt.Sprintf(format, args...)} }

// runTool executes a tool call on behalf of userID and returns the JSON result
// for the model. Errors the model can react to are encoded into the result;
// only infrastructure failures are returned as error.
func (a *Agent) runTool(ctx context.Context, userID int64, call *llm.ToolCall) (string, error) {
	scope := expense.OwnedBy(userID)
	var (
		result any
		err    error
	)
	switch call.Name {
	case toolAdd:
		result, err = a.addExpense(ctx, userID, call.Arguments)
	case toolFind:
		result, err = a.findExpenses(ctx, scope, call.Arguments)
	case toolSummary:
		result, err = a.summary(ctx, scope, call.Arguments)
	case toolUpdate:
		result, err = a.updateExpense(ctx, scope, call.Arguments)
	case toolDelete:
		result, err = a.deleteExpense(ctx, scope, call.Arguments)
	default:
		err = badArgs("unknown function %q", call.Name)
	}

	var (
		terr *toolError
		verr *expense.ValidationError
	)
	switch {
	case err == nil:
	case errors.As(err, &terr):
		result = map[string]string{"error": terr.msg}
	case errors.As(err, &verr):
		result = map[string]string{"error": "invalid " + verr.Field + ": " + verr.Message}
	case errors.Is(err, expense.ErrNotFound):
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

func (a *Agent) addExpense(ctx context.Context, userID int64, raw json.RawMessage) (any, error) {
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
	e, err := a.expenses.Create(ctx, expense.CreateInput{
		UserID:   userID, // from Telegram, never from the model
		Amount:   *amount,
		Category: *category,
		SpentAt:  date,
		Comment:  args.Comment,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"added": toItem(e)}, nil
}

type findArgs struct {
	DateFrom  string `json:"date_from"`
	DateTo    string `json:"date_to"`
	Category  string `json:"category"`
	Text      string `json:"text"`
	LastAdded bool   `json:"last_added"`
	Limit     int    `json:"limit"`
}

func (a *Agent) findExpenses(ctx context.Context, scope expense.Scope, raw json.RawMessage) (any, error) {
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
		f.Sort = expense.SortByCreatedAt
	}
	page, err := a.expenses.List(ctx, scope, f)
	if err != nil {
		return nil, err
	}
	items := make([]item, len(page.Items))
	for i, e := range page.Items {
		items[i] = toItem(e)
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

func (a *Agent) summary(ctx context.Context, scope expense.Scope, raw json.RawMessage) (any, error) {
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

func (a *Agent) updateExpense(ctx context.Context, scope expense.Scope, raw json.RawMessage) (any, error) {
	var args updateArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	id, err := parseID(args.ID)
	if err != nil {
		return nil, err
	}
	var p expense.Patch
	if p.Amount, err = parseAmount(args.Amount, false); err != nil {
		return nil, err
	}
	if p.Category, err = parseCategory(args.Category, false); err != nil {
		return nil, err
	}
	if p.SpentAt, err = parseDate("date", args.Date); err != nil {
		return nil, err
	}
	p.Comment = args.Comment
	if p.Amount == nil && p.Category == nil && p.SpentAt == nil && p.Comment == nil {
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
	return map[string]any{"before": toItem(before), "after": toItem(after)}, nil
}

type deleteArgs struct {
	ID json.Number `json:"id"`
}

func (a *Agent) deleteExpense(ctx context.Context, scope expense.Scope, raw json.RawMessage) (any, error) {
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
	return map[string]any{"deleted": toItem(e)}, nil
}

// item is the compact representation of an expense shown to the model.
type item struct {
	ID       int64       `json:"id"`
	Amount   json.Number `json:"amount"`
	Category string      `json:"category"`
	Date     string      `json:"date"`
	Comment  string      `json:"comment,omitempty"`
}

func toItem(e expense.Expense) item {
	return item{
		ID:       e.ID,
		Amount:   money(e.Amount),
		Category: e.Category.Label(),
		Date:     e.SpentAt.Format(time.DateOnly),
		Comment:  e.Comment,
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
var categoryAliases = func() map[string]expense.Category {
	m := map[string]expense.Category{}
	for _, c := range expense.Categories {
		m[string(c)] = c
		m[c.Label()] = c
		m[strings.ReplaceAll(c.Label(), "ё", "е")] = c
	}
	return m
}()

func parseCategory(s string, required bool) (*expense.Category, error) {
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

func periodFilter(from, to, category string) (expense.Filter, error) {
	var (
		f   expense.Filter
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
