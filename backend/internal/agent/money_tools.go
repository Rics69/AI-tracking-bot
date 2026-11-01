package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/accounts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/tasks"
)

const (
	toolAddTransfer = "add_transfer"
	toolAddTask     = "add_task"
	toolListTasks   = "list_tasks"
)

// Accounts is the part of accounts.Service the agent needs.
type Accounts interface {
	ByName(ctx context.Context, userID int64, name string) (accounts.Account, error)
	Names(ctx context.Context, userID int64) ([]string, error)
	Transfer(ctx context.Context, in accounts.TransferInput) (accounts.Transfer, error)
}

// Tasks is the part of tasks.Service the agent needs.
type Tasks interface {
	Create(ctx context.Context, t tasks.Task, today time.Time) (tasks.Task, error)
	Calendar(ctx context.Context, userID int64, from, to time.Time) ([]tasks.Occurrence, error)
}

// moneyRules explain accounts, currencies and tasks. Like budgetRules, they go
// before everything that changes from request to request.
const moneyRules = `
16. Сумма в другой валюте («кофе 5 долларов», «такси 10 евро») — add_expense или add_income с currency и суммой в этой валюте: USD или EUR. Переводить в рубли не нужно, это сделает код по курсу ЦБ на дату операции.
    Счёт называй, только если его назвал пользователь: «оплатил наличкой» — параметр account. Если счёт не назван, не передавай его.
    Названия счетов смотри в списке ниже. Если подходящего счёта нет, спроси.
17. Перемещение денег между своими счетами («перевёл с карты на накопительный 5000», «снял наличку 3000», «купил 100 долларов за 8000») — add_transfer, а не трата и не доход.
    Перевод другому человеку — это трата: add_expense.
18. «Напомни в пятницу оплатить интернет», «запланируй на 5 число», «каждый понедельник проверять бюджет» — add_task. Задача может быть в будущем; дату бери из списка ближайших дней.
    Запрет на будущие даты из правила 4 относится только к операциям. Время передавай, только если его назвал пользователь.
    «Что у меня запланировано», «какие задачи на неделю» — list_tasks.`

func moneyToolDefs(today time.Time) []llm.Tool {
	d := func(t time.Time) string { return t.Format(time.DateOnly) }
	return []llm.Tool{
		{
			Name: toolAddTransfer,
			Description: "Записать перемещение денег между счетами пользователя. Это не трата и не доход, в сводки и лимит не попадает. " +
				"Покупка валюты — тоже перевод: amount_to в валюте счёта-получателя.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "from_account": {"type": "string", "description": "Название счёта, откуда ушли деньги"},
    "to_account": {"type": "string", "description": "Название счёта, куда пришли деньги"},
    "amount": {"type": "number", "description": "Сколько ушло, в валюте счёта-отправителя"},
    "amount_to": {"type": "number", "description": "Сколько пришло, если у счетов разная валюта"},
    "date": {"type": "string", "description": "Дата YYYY-MM-DD"},
    "comment": {"type": "string", "description": "Комментарий, если есть"}
  },
  "required": ["from_account", "to_account", "amount", "date"]
}`),
			Examples: []llm.ToolExample{
				{Request: "перевёл с Основной на Накопительный 5000", Params: map[string]any{"from_account": "Основной", "to_account": "Накопительный", "amount": 5000, "date": d(today)}},
				{Request: "купил 100 долларов за 8000 (счёт Доллары)", Params: map[string]any{"from_account": "Основной", "to_account": "Доллары", "amount": 8000, "amount_to": 100, "date": d(today)}},
			},
		},
		{
			Name:        toolAddTask,
			Description: "Запланировать задачу или напоминание: дата может быть в будущем.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "title": {"type": "string", "description": "Что сделать, коротко"},
    "date": {"type": "string", "description": "Дата YYYY-MM-DD"},
    "time": {"type": "string", "description": "Время ЧЧ:ММ, если названо"},
    "repeat": {"type": "string", "enum": ["none", "daily", "weekdays", "weekly", "monthly"], "description": "Повтор, если он назван"}
  },
  "required": ["title", "date"]
}`),
			Examples: []llm.ToolExample{
				{Request: "напомни завтра оплатить интернет", Params: map[string]any{"title": "оплатить интернет", "date": d(today.AddDate(0, 0, 1))}},
				{Request: "напомни завтра в 10:30 позвонить в банк", Params: map[string]any{"title": "позвонить в банк", "date": d(today.AddDate(0, 0, 1)), "time": "10:30"}},
			},
		},
		{
			Name:        toolListTasks,
			Description: "Показать запланированные задачи за период.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "date_from": {"type": "string", "description": "Начало периода YYYY-MM-DD"},
    "date_to": {"type": "string", "description": "Конец периода YYYY-MM-DD, не больше двух месяцев от начала"}
  },
  "required": ["date_from", "date_to"]
}`),
			Examples: []llm.ToolExample{
				{Request: "что у меня запланировано на неделю", Params: map[string]any{"date_from": d(today), "date_to": d(today.AddDate(0, 0, 7))}},
			},
		},
	}
}

// upcoming lists the coming days with weekdays, so "в пятницу" of a task is
// found by lookup, as the past days of operations are.
func upcoming(today time.Time) string {
	days := make([]string, 7)
	for i := range days {
		d := today.AddDate(0, 0, i+1)
		days[i] = weekdaysShort[d.Weekday()] + " " + d.Format(time.DateOnly)
	}
	return strings.Join(days, ", ")
}

// resolveAccount turns a name from the model into an account of the user.
func (a *Agent) resolveAccount(ctx context.Context, t turn, name string) (*accounts.Account, error) {
	if name == "" || a.accounts == nil {
		return nil, nil
	}
	acc, err := a.accounts.ByName(ctx, t.userID, name)
	if errors.Is(err, accounts.ErrNotFound) {
		names, _ := a.accounts.Names(ctx, t.userID)
		return nil, badArgs("no account %q; the user's accounts: %s. Ask the user which one", name, strings.Join(names, ", "))
	}
	if err != nil {
		return nil, err
	}
	return &acc, nil
}

type transferArgs struct {
	From     string      `json:"from_account"`
	To       string      `json:"to_account"`
	Amount   json.Number `json:"amount"`
	AmountTo json.Number `json:"amount_to"`
	Date     string      `json:"date"`
	Comment  string      `json:"comment"`
}

func (a *Agent) addTransfer(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	if a.accounts == nil {
		return nil, badArgs("accounts are not available")
	}
	var args transferArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	from, err := a.resolveAccount(ctx, t, args.From)
	if err != nil {
		return nil, err
	}
	to, err := a.resolveAccount(ctx, t, args.To)
	if err != nil {
		return nil, err
	}
	if from == nil || to == nil {
		return nil, badArgs("from_account and to_account are required")
	}
	amount, err := parseAmount(args.Amount, true)
	if err != nil {
		return nil, err
	}
	amountTo, err := parseAmount(args.AmountTo, false)
	if err != nil {
		return nil, err
	}
	date, err := parseDate("date", args.Date)
	if err != nil {
		return nil, err
	}
	in := accounts.TransferInput{UserID: t.userID, FromID: from.ID, ToID: to.ID, AmountFrom: *amount, Comment: args.Comment, Today: t.today}
	if amountTo != nil {
		in.AmountTo = *amountTo
	}
	if date != nil {
		in.Date = *date
	}
	tr, err := a.accounts.Transfer(ctx, in)
	if err != nil {
		return nil, err
	}
	return map[string]any{"transferred": map[string]any{
		"from": from.Name, "to": to.Name, "amount": money(tr.AmountFrom), "currency_from": from.Currency,
		"amount_to": money(tr.AmountTo), "currency_to": to.Currency, "date_text": HumanDate(tr.Date, t.today),
	}}, nil
}

type taskArgs struct {
	Title  string `json:"title"`
	Date   string `json:"date"`
	Time   string `json:"time"`
	Repeat string `json:"repeat"`
}

var loneHour = regexp.MustCompile(`^\d:\d\d$`)

func (a *Agent) addTask(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	if a.tasks == nil {
		return nil, badArgs("tasks are not available")
	}
	var args taskArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	date, err := parseDate("date", args.Date)
	if err != nil {
		return nil, err
	}
	if loneHour.MatchString(args.Time) {
		args.Time = "0" + args.Time
	}
	task := tasks.Task{UserID: t.userID, Title: args.Title, DueTime: args.Time, Repeat: args.Repeat}
	if date != nil {
		task.DueOn = *date
	}
	saved, err := a.tasks.Create(ctx, task, t.today)
	if err != nil {
		return nil, err
	}
	a.events.Track(ctx, t.userID, events.TaskCreated, events.Props{"via": events.ViaAI})
	return map[string]any{"added": taskItem(saved, saved.DueOn, t.today)}, nil
}

func (a *Agent) listTasks(ctx context.Context, t turn, raw json.RawMessage) (any, error) {
	if a.tasks == nil {
		return nil, badArgs("tasks are not available")
	}
	var args struct {
		From string `json:"date_from"`
		To   string `json:"date_to"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	from, err := parseDate("date_from", args.From)
	if err != nil {
		return nil, err
	}
	to, err := parseDate("date_to", args.To)
	if err != nil {
		return nil, err
	}
	if from == nil || to == nil {
		return nil, badArgs("date_from and date_to are required")
	}
	list, err := a.tasks.Calendar(ctx, t.userID, *from, *to)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, len(list))
	for i, o := range list {
		items[i] = taskItem(o.Task, o.Date, t.today)
		items[i]["done"] = o.Done
	}
	return map[string]any{"tasks": items}, nil
}

func taskItem(task tasks.Task, date, today time.Time) map[string]any {
	item := map[string]any{"title": task.Title, "date_text": HumanDate(date, today)}
	if task.DueTime != "" {
		item["time"] = task.DueTime
	}
	if task.Repeat != tasks.RepeatNone {
		item["repeat"] = task.Repeat
	}
	return item
}

// paidText is what an operation in another currency cost, for the reply.
func paidText(e ledger.Operation) string {
	if e.Original == nil {
		return ""
	}
	return fmt.Sprintf("%s %s", e.Original.StringFixedBank(2), e.Currency)
}
