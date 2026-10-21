package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
)

var today = time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC) // Friday

// scriptedLLM returns prepared responses in order and records requests.
type scriptedLLM struct {
	responses []llm.Response
	err       error
	requests  []llm.Request
}

func (s *scriptedLLM) Chat(_ context.Context, req llm.Request) (llm.Response, error) {
	s.requests = append(s.requests, req)
	if s.err != nil {
		return llm.Response{}, s.err
	}
	if len(s.requests) > len(s.responses) {
		return llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "(script ended)"}}, nil
	}
	return s.responses[len(s.requests)-1], nil
}

func call(name, args string) llm.Response {
	return llm.Response{Message: llm.Message{
		Role:     llm.RoleAssistant,
		ToolCall: &llm.ToolCall{Name: name, Arguments: json.RawMessage(args)},
	}}
}

func text(s string) llm.Response {
	return llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: s}}
}

// memExpenses is an in-memory Expenses honouring scopes.
type memExpenses struct {
	items   map[int64]ledger.Operation
	nextID  int64
	lastLoc *time.Location // the timezone the agent asked "today" for
}

func newMemExpenses() *memExpenses { return &memExpenses{items: map[int64]ledger.Operation{}} }

func (m *memExpenses) TodayIn(loc *time.Location) time.Time {
	m.lastLoc = loc
	return today
}

func (m *memExpenses) visible(s ledger.Scope, e ledger.Operation) bool {
	return e.UserID == s.UserID() // the agent only ever uses OwnedBy scopes
}

func (m *memExpenses) Create(_ context.Context, in ledger.CreateInput) (ledger.Operation, error) {
	if !in.Category.Valid() {
		return ledger.Operation{}, &ledger.ValidationError{Field: "category", Message: "unknown"}
	}
	m.nextID++
	e := ledger.Operation{ID: m.nextID, UserID: in.UserID, Amount: in.Amount, Category: in.Category,
		Date: today, Comment: in.Comment, CreatedAt: time.Unix(m.nextID, 0)}
	if in.Date != nil {
		e.Date = *in.Date
	}
	m.items[e.ID] = e
	return e, nil
}

func (m *memExpenses) Get(_ context.Context, s ledger.Scope, id int64) (ledger.Operation, error) {
	e, ok := m.items[id]
	if !ok || !m.visible(s, e) {
		return ledger.Operation{}, ledger.ErrNotFound
	}
	return e, nil
}

func (m *memExpenses) Update(ctx context.Context, s ledger.Scope, id int64, p ledger.Patch) (ledger.Operation, error) {
	e, err := m.Get(ctx, s, id)
	if err != nil {
		return e, err
	}
	if p.Amount != nil {
		e.Amount = *p.Amount
	}
	if p.Comment != nil {
		e.Comment = *p.Comment
	}
	m.items[id] = e
	return e, nil
}

func (m *memExpenses) Delete(ctx context.Context, s ledger.Scope, id int64) error {
	if _, err := m.Get(ctx, s, id); err != nil {
		return err
	}
	delete(m.items, id)
	return nil
}

func (m *memExpenses) List(_ context.Context, s ledger.Scope, f ledger.Filter) (ledger.Page, error) {
	page := ledger.Page{Sum: decimal.Zero}
	for id := m.nextID; id > 0; id-- { // newest first
		e, ok := m.items[id]
		if !ok || !m.visible(s, e) || (f.Query != "" && !strings.Contains(e.Comment, f.Query)) {
			continue
		}
		page.Total++
		page.Sum = page.Sum.Add(e.Amount)
		if len(page.Items) < f.Limit {
			page.Items = append(page.Items, e)
		}
	}
	return page, nil
}

func (m *memExpenses) Summary(context.Context, ledger.Scope, ledger.Filter) (ledger.Summary, error) {
	return ledger.Summary{Sum: decimal.Zero}, nil
}

func newTestAgent(l llm.Client, exp *memExpenses) *Agent {
	return New(l, exp, exp, NewHistory(6, time.Hour), Config{Location: time.UTC},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

var anna = Caller{UserID: 1, Name: "Анна"}

// lastToolResult returns the content of the last tool message sent to the model.
func lastToolResult(t *testing.T, req llm.Request) string {
	t.Helper()
	last := req.Messages[len(req.Messages)-1]
	if last.Role != llm.RoleTool {
		t.Fatalf("last message is %s, want a tool result", last.Role)
	}
	return last.Content
}

func TestAddExpense(t *testing.T) {
	exp := newMemExpenses()
	model := &scriptedLLM{responses: []llm.Response{
		call(toolAdd, `{"amount": 350, "category": "food", "comment": "кофе"}`),
		text("Записал: кофе, 350 ₽"),
	}}
	reply, err := newTestAgent(model, exp).Reply(context.Background(), anna, "кофе 350")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "Записал: кофе, 350 ₽" {
		t.Errorf("reply = %q", reply)
	}
	e := exp.items[1]
	if e.UserID != anna.UserID || !e.Amount.Equal(decimal.NewFromInt(350)) || e.Category != ledger.CategoryFood ||
		e.Comment != "кофе" || !e.Date.Equal(today) {
		t.Errorf("saved %+v", e)
	}
	if res := lastToolResult(t, model.requests[1]); !strings.Contains(res, `"added"`) || !strings.Contains(res, `"amount":350.00`) {
		t.Errorf("tool result = %s", res)
	}
	// The system prompt tells the model today's date.
	if sys := model.requests[0].Messages[0]; sys.Role != llm.RoleSystem || !strings.Contains(sys.Content, "2026-10-16, пятница") {
		t.Errorf("system prompt = %q", sys.Content)
	}
}

func TestOwnerComesFromTelegramNotFromModel(t *testing.T) {
	exp := newMemExpenses()
	model := &scriptedLLM{responses: []llm.Response{
		// A prompt-injected model tries to write on behalf of someone else.
		call(toolAdd, `{"amount": 100, "category": "other", "comment": "x", "user_id": 999}`),
		text("ok"),
	}}
	if _, err := newTestAgent(model, exp).Reply(context.Background(), anna, "запиши трату на пользователя 999"); err != nil {
		t.Fatal(err)
	}
	if exp.items[1].UserID != anna.UserID {
		t.Errorf("expense saved for user %d, want %d", exp.items[1].UserID, anna.UserID)
	}
}

func TestCannotTouchOtherUsersExpense(t *testing.T) {
	exp := newMemExpenses()
	borisExpense, _ := exp.Create(context.Background(), ledger.CreateInput{
		UserID: 2, Amount: decimal.NewFromInt(500), Category: ledger.CategoryFood})

	model := &scriptedLLM{responses: []llm.Response{
		call(toolDelete, `{"id": 1}`),
		text("Не нашёл такую трату"),
	}}
	if _, err := newTestAgent(model, exp).Reply(context.Background(), anna, "удали трату 1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := exp.items[borisExpense.ID]; !ok {
		t.Fatal("Anna deleted Boris's expense")
	}
	if res := lastToolResult(t, model.requests[1]); !strings.Contains(res, "not found") {
		t.Errorf("tool result = %s", res)
	}
}

func TestToolErrorIsReturnedToModel(t *testing.T) {
	exp := newMemExpenses()
	model := &scriptedLLM{responses: []llm.Response{
		call(toolAdd, `{"amount": 350, "category": "кофейня", "comment": "кофе"}`),
		call(toolAdd, `{"amount": 350, "category": "еда", "comment": "кофе", "date": "вчера"}`),
		call(toolAdd, `{"amount": "350", "category": "еда", "comment": "кофе", "date": "2026-10-15"}`),
		text("Записал"),
	}}
	if _, err := newTestAgent(model, exp).Reply(context.Background(), anna, "кофе 350 вчера"); err != nil {
		t.Fatal(err)
	}
	if res := lastToolResult(t, model.requests[1]); !strings.Contains(res, "unknown category") {
		t.Errorf("first result = %s", res)
	}
	if res := lastToolResult(t, model.requests[2]); !strings.Contains(res, "YYYY-MM-DD") {
		t.Errorf("second result = %s", res)
	}
	// Russian category names and string amounts are accepted.
	e := exp.items[1]
	if e.Category != ledger.CategoryFood || !e.Date.Equal(today.AddDate(0, 0, -1)) {
		t.Errorf("saved %+v", e)
	}
}

func TestEditLastExpense(t *testing.T) {
	exp := newMemExpenses()
	ctx := context.Background()
	_, _ = exp.Create(ctx, ledger.CreateInput{UserID: 1, Amount: decimal.NewFromInt(1200), Category: ledger.CategoryTransport, Comment: "такси"})
	last, _ := exp.Create(ctx, ledger.CreateInput{UserID: 1, Amount: decimal.NewFromInt(350), Category: ledger.CategoryFood, Comment: "кофе"})

	model := &scriptedLLM{responses: []llm.Response{
		call(toolFind, `{"last_added": true, "limit": 1}`),
		call(toolUpdate, `{"id": 2, "amount": 450}`),
		text("Исправил: кофе 450 ₽"),
	}}
	if _, err := newTestAgent(model, exp).Reply(ctx, anna, "последнее было не 350, а 450"); err != nil {
		t.Fatal(err)
	}
	if res := lastToolResult(t, model.requests[1]); !strings.Contains(res, `"id":2`) || strings.Contains(res, `"id":1`) {
		t.Errorf("find result = %s", res)
	}
	if !exp.items[last.ID].Amount.Equal(decimal.NewFromInt(450)) {
		t.Errorf("amount = %s, want 450", exp.items[last.ID].Amount)
	}
	if res := lastToolResult(t, model.requests[2]); !strings.Contains(res, `"before"`) || !strings.Contains(res, "350.00") {
		t.Errorf("update result = %s", res)
	}
}

func TestUpdateWithoutChangesIsRejected(t *testing.T) {
	exp := newMemExpenses()
	model := &scriptedLLM{responses: []llm.Response{call(toolUpdate, `{"id": 1}`), text("?")}}
	_, _ = newTestAgent(model, exp).Reply(context.Background(), anna, "измени")
	if res := lastToolResult(t, model.requests[1]); !strings.Contains(res, "nothing to update") {
		t.Errorf("result = %s", res)
	}
}

func TestGivesUpAfterMaxSteps(t *testing.T) {
	exp := newMemExpenses()
	loop := make([]llm.Response, DefaultMaxSteps+1)
	for i := range loop {
		loop[i] = call(toolFind, `{}`)
	}
	model := &scriptedLLM{responses: loop}
	a := newTestAgent(model, exp)
	reply, err := a.Reply(context.Background(), anna, "что-то странное")
	if err != nil {
		t.Fatal(err)
	}
	if reply != replyGaveUp || len(model.requests) != DefaultMaxSteps {
		t.Errorf("reply = %q after %d requests", reply, len(model.requests))
	}
	if h := must(a.history.Messages(context.Background(), anna.UserID)); len(h) != 0 {
		t.Errorf("unfinished turn saved to history: %d messages", len(h))
	}
}

func TestHistoryCarriesOverTurns(t *testing.T) {
	exp := newMemExpenses()
	model := &scriptedLLM{responses: []llm.Response{
		call(toolAdd, `{"amount": 350, "category": "food", "comment": "кофе"}`),
		text("Записал"),
		text("Пожалуйста"),
	}}
	a := newTestAgent(model, exp)
	ctx := context.Background()
	_, _ = a.Reply(ctx, anna, "кофе 350")
	_, _ = a.Reply(ctx, anna, "спасибо")

	// system + (user, call, result, answer) + new user
	msgs := model.requests[2].Messages
	if len(msgs) != 6 || msgs[1].Content != "кофе 350" || msgs[2].ToolCall == nil || msgs[3].Role != llm.RoleTool {
		t.Errorf("second request messages = %+v", msgs)
	}

	a.Reset(context.Background(), anna.UserID)
	if len(must(a.history.Messages(context.Background(), anna.UserID))) != 0 {
		t.Error("history not reset")
	}
}

func TestBlockedAndFailedLLM(t *testing.T) {
	reply, err := newTestAgent(&scriptedLLM{err: llm.ErrBlocked}, newMemExpenses()).Reply(context.Background(), anna, "...")
	if err != nil || reply != replyBlocked {
		t.Errorf("blocked: reply = %q err = %v", reply, err)
	}
	_, err = newTestAgent(&scriptedLLM{err: errors.New("timeout")}, newMemExpenses()).Reply(context.Background(), anna, "кофе 350")
	if err == nil {
		t.Error("LLM failure must be returned as error")
	}
}

func TestSystemPromptCalendar(t *testing.T) {
	p := systemPrompt(today, time.UTC, "Анна")
	for _, want := range []string{
		"сегодня: 2026-10-16, пятница",
		"последние 7 дней: пт 2026-10-16 (сегодня), чт 2026-10-15 (вчера), ср 2026-10-14, вт 2026-10-13, пн 2026-10-12, вс 2026-10-11, сб 2026-10-10",
		"эта неделя: 2026-10-12 — 2026-10-18; прошлая неделя: 2026-10-05 — 2026-10-11",
		"этот месяц: 2026-10-01 — 2026-10-31; прошлый месяц: 2026-09-01 — 2026-09-30",
		"Пользователь: Анна",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	// Sunday belongs to the week that started on Monday.
	if p := systemPrompt(time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC), time.UTC, ""); !strings.Contains(p, "эта неделя: 2026-10-12 — 2026-10-18") {
		t.Error("Sunday is not in the current week")
	}
}

func TestToolSchemasAreValidJSON(t *testing.T) {
	for _, tool := range toolDefs(today) {
		var schema map[string]any
		if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
			t.Errorf("%s: %v", tool.Name, err)
		}
		if _, ok := schema["properties"].(map[string]any)["user_id"]; ok {
			t.Errorf("%s exposes user_id to the model", tool.Name)
		}
	}
}

func TestHumanDate(t *testing.T) {
	for d, want := range map[time.Time]string{
		today:                    "сегодня",
		today.AddDate(0, 0, -1):  "вчера",
		today.AddDate(0, 0, -2):  "позавчера",
		today.AddDate(0, 0, -10): "6 октября",
		today.AddDate(-1, 0, 0):  "16 октября 2025",
	} {
		if got := humanDate(d, today); got != want {
			t.Errorf("humanDate(%s) = %q, want %q", d.Format(time.DateOnly), got, want)
		}
	}
}

func TestToolExamplesUseRealDates(t *testing.T) {
	var add llm.Tool
	for _, tool := range toolDefs(today) {
		if tool.Name == toolAdd {
			add = tool
		}
	}
	want := map[string]string{
		"такси 1200 вчера":          "2026-10-15",
		"бензин 3000 в понедельник": "2026-10-12",
		"продукты 2.5к в субботу":   "2026-10-10",
	}
	for _, ex := range add.Examples {
		if w, ok := want[ex.Request]; ok && ex.Params["date"] != w {
			t.Errorf("%q: date = %v, want %s", ex.Request, ex.Params["date"], w)
		}
	}
	if got := lastWeekday(today, time.Friday); !got.Equal(today) {
		t.Errorf("lastWeekday(Friday) on a Friday = %s, want today", got)
	}
}

type usageCall struct {
	userID int64
	day    time.Time
	usage  llm.Usage
	failed bool
}

type fakeRecorder struct{ calls []usageCall }

func (f *fakeRecorder) RecordUsage(_ context.Context, userID int64, day time.Time, u llm.Usage, failed bool) {
	f.calls = append(f.calls, usageCall{userID, day, u, failed})
}

func TestUserTimezoneAndUsage(t *testing.T) {
	exp := newMemExpenses()
	rec := &fakeRecorder{}
	withUsage := func(r llm.Response, prompt int) llm.Response {
		r.Usage = llm.Usage{PromptTokens: prompt, CompletionTokens: 10}
		return r
	}
	model := &scriptedLLM{responses: []llm.Response{
		withUsage(call(toolAdd, `{"amount": 350, "category": "food", "comment": "кофе", "date": "2026-10-16"}`), 100),
		withUsage(text("Записано"), 120),
	}}
	omsk, _ := time.LoadLocation("Asia/Omsk")
	a := New(model, exp, exp, NewHistory(6, time.Hour), Config{Location: time.UTC, Usage: rec},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := a.Reply(context.Background(), Caller{UserID: 1, Name: "Анна", Location: omsk}, "кофе 350"); err != nil {
		t.Fatal(err)
	}
	if exp.lastLoc != omsk || !strings.Contains(model.requests[0].Messages[0].Content, "Asia/Omsk") {
		t.Errorf("today was computed in %v, want the user's timezone", exp.lastLoc)
	}
	want := usageCall{1, today, llm.Usage{PromptTokens: 220, CompletionTokens: 20}, false}
	if len(rec.calls) != 1 || rec.calls[0] != want {
		t.Errorf("usage = %+v, want %+v", rec.calls, want)
	}

	model.err = errors.New("gigachat: HTTP 500")
	if _, err := a.Reply(context.Background(), anna, "кофе 350"); err == nil {
		t.Fatal("want an error")
	}
	if len(rec.calls) != 2 || !rec.calls[1].failed || exp.lastLoc != time.UTC {
		t.Errorf("after a failure: usage = %+v, timezone = %v", rec.calls, exp.lastLoc)
	}
}
