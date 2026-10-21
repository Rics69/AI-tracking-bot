package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// fakeExpenses records calls and returns canned results.
type fakeExpenses struct {
	err        error
	op         *ledger.Operation // returned by Get instead of sample
	lastScope  ledger.Scope
	lastFilter ledger.Filter
	lastCreate ledger.CreateInput
	lastPatch  ledger.Patch
}

var sample = ledger.Operation{
	ID: 7, UserID: 111, Amount: decimal.RequireFromString("350.50"), Category: ledger.CategoryFood,
	Date: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), Comment: "кофе",
}

func (f *fakeExpenses) Create(_ context.Context, in ledger.CreateInput) (ledger.Operation, error) {
	f.lastCreate = in
	return sample, f.err
}

func (f *fakeExpenses) Get(_ context.Context, s ledger.Scope, _ int64) (ledger.Operation, error) {
	f.lastScope = s
	if f.op != nil {
		return *f.op, f.err
	}
	return sample, f.err
}

func (f *fakeExpenses) Update(_ context.Context, s ledger.Scope, _ int64, p ledger.Patch) (ledger.Operation, error) {
	f.lastScope, f.lastPatch = s, p
	return sample, f.err
}

func (f *fakeExpenses) Delete(_ context.Context, s ledger.Scope, _ int64) error {
	f.lastScope = s
	return f.err
}

func (f *fakeExpenses) List(_ context.Context, s ledger.Scope, flt ledger.Filter) (ledger.Page, error) {
	f.lastScope, f.lastFilter = s, flt
	return ledger.Page{Items: []ledger.Operation{sample}, Total: 1, ExpenseSum: sample.Amount}, f.err
}

func (f *fakeExpenses) TimeSeries(_ context.Context, s ledger.Scope, flt ledger.Filter, b ledger.Bucket) ([]ledger.Point, error) {
	f.lastScope, f.lastFilter = s, flt
	return []ledger.Point{
		{Start: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), Sum: decimal.NewFromInt(300), Count: 1,
			Categories: []ledger.CategoryTotal{{Category: ledger.CategoryFood, Sum: decimal.NewFromInt(300), Count: 1}}},
		{Start: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC), Sum: decimal.Zero, Categories: []ledger.CategoryTotal{}},
	}, f.err
}

func (f *fakeExpenses) Summary(_ context.Context, s ledger.Scope, flt ledger.Filter) (ledger.Summary, error) {
	f.lastScope, f.lastFilter = s, flt
	return ledger.Summary{}, f.err
}

// head is the panel's viewer in tests: the head of family 10.
var head = user.User{TelegramID: 1, FamilyID: 10, FirstName: "Анна", IsHead: true}

// stranger is a Telegram user outside family 10.
const stranger = 9

// fakeFamily returns canned members and invites, or err from every call.
type fakeFamily struct {
	err     error
	removed []int64
}

func (f *fakeFamily) Member(_ context.Context, familyID, id int64) (user.User, error) {
	if familyID != head.FamilyID || id == stranger {
		return user.User{}, family.ErrNotMember
	}
	return user.User{TelegramID: id, FamilyID: familyID}, f.err
}

func (f *fakeFamily) Members(context.Context, int64) ([]user.User, error) {
	return []user.User{head, {TelegramID: 2, FamilyID: 10, FirstName: "Борис"}}, f.err
}

func (f *fakeFamily) Remove(_ context.Context, _ user.User, id int64) error {
	f.removed = append(f.removed, id)
	return f.err
}

func (f *fakeFamily) CreateInvite(context.Context, user.User) (family.CreatedInvite, error) {
	return family.CreatedInvite{
		Invite: family.Invite{ID: 5, ExpiresAt: time.Now().Add(family.InviteTTL)},
		Link:   "https://t.me/FamilyBot?start=abc",
	}, f.err
}

func (f *fakeFamily) ListInvites(context.Context, int64) ([]family.Invite, error) {
	past := time.Now().Add(-time.Hour)
	return []family.Invite{
		{ID: 1, ExpiresAt: time.Now().Add(time.Hour)},
		{ID: 2, ExpiresAt: past},
	}, f.err
}

func (f *fakeFamily) RevokeInvite(context.Context, int64, int64) error { return f.err }

func (f *fakeFamily) SetWeeklyReport(_ context.Context, _ user.User, id int64, on bool) (user.User, error) {
	return user.User{TelegramID: id, FirstName: "Анна", WeeklyReport: on}, f.err
}
func (f *fakeFamily) Now() time.Time { return time.Now() }

func newTestHandler(f *fakeExpenses, health error) http.Handler {
	return newTestHandlerWithFamily(f, &fakeFamily{}, health)
}

func newTestHandlerWithFamily(f *fakeExpenses, fam *fakeFamily, health error) http.Handler {
	return newTestHandlerFor(f, fam, health, func(context.Context) (user.User, error) { return head, nil })
}

func newTestHandlerFor(f *fakeExpenses, fam *fakeFamily, health error, viewer Viewer) http.Handler {
	return NewHandler(Deps{
		Expenses: f,
		Family:   fam,
		Viewer:   viewer,
		Health:   func(context.Context) error { return health },
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func do(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("response is not JSON: %q", rec.Body.String())
		}
	}
	return rec.Code, out
}

func errorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestCreateExpense(t *testing.T) {
	f := &fakeExpenses{}
	status, body := do(t, newTestHandler(f, nil), http.MethodPost, "/api/v1/expenses",
		`{"user_id":111,"amount":350.5,"category":"food","spent_at":"2026-10-14","comment":"кофе"}`)

	if status != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", status, body)
	}
	// Amount is a JSON number, the date has no time part.
	if body["amount"] != 350.5 || body["spent_at"] != "2026-10-15" || body["comment"] != "кофе" {
		t.Errorf("body = %v", body)
	}
	in := f.lastCreate
	if in.UserID != 111 || !in.Amount.Equal(decimal.RequireFromString("350.5")) || in.Category != ledger.CategoryFood ||
		in.Date == nil || in.Date.Format(time.DateOnly) != "2026-10-14" || in.Comment != "кофе" {
		t.Errorf("service got %+v", in)
	}
}

func TestListExpensesPassesFilter(t *testing.T) {
	f := &fakeExpenses{}
	status, body := do(t, newTestHandler(f, nil), http.MethodGet,
		"/api/v1/expenses?user_id=111&category=food&from=2026-10-01&to=2026-10-31&q=кофе&limit=5&offset=10", "")

	if status != http.StatusOK || body["total"] != float64(1) {
		t.Fatalf("status = %d, body = %v", status, body)
	}
	flt := f.lastFilter
	if *flt.UserID != 111 || *flt.Category != ledger.CategoryFood ||
		flt.From.Format(time.DateOnly) != "2026-10-01" || flt.To.Format(time.DateOnly) != "2026-10-31" ||
		flt.Query != "кофе" || flt.Limit != 5 || flt.Offset != 10 {
		t.Errorf("filter = %+v", flt)
	}
	if f.lastScope.FamilyID() != head.FamilyID {
		t.Errorf("scope = %+v, want the head's family", f.lastScope)
	}
}

func TestUpdateExpenseSendsOnlyGivenFields(t *testing.T) {
	f := &fakeExpenses{}
	status, _ := do(t, newTestHandler(f, nil), http.MethodPatch, "/api/v1/expenses/7", `{"amount":450}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	p := f.lastPatch
	if p.Amount == nil || !p.Amount.Equal(decimal.NewFromInt(450)) ||
		p.Category != nil || p.Comment != nil || p.Date != nil || p.UserID != nil {
		t.Errorf("patch = %+v", p)
	}
}

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		method     string
		path, body string
		status     int
		code       string
	}{
		{"validation", &ledger.ValidationError{Field: "amount", Message: "must be greater than zero"},
			http.MethodPost, "/api/v1/expenses", `{"user_id":1,"amount":0,"category":"food"}`, 400, "validation_error"},
		{"not found", ledger.ErrNotFound, http.MethodGet, "/api/v1/expenses/1", "", 404, "not_found"},
		{"delete not found", ledger.ErrNotFound, http.MethodDelete, "/api/v1/expenses/1", "", 404, "not_found"},
		{"unknown user", ledger.ErrUnknownUser,
			http.MethodPost, "/api/v1/expenses", `{"user_id":9,"amount":1,"category":"food"}`, 400, "unknown_user"},
		{"unexpected", errors.New("connection refused"), http.MethodGet, "/api/v1/expenses", "", 500, "internal"},
		{"malformed json", nil, http.MethodPost, "/api/v1/expenses", `{"user_id":`, 400, "bad_request"},
		{"bad id", nil, http.MethodGet, "/api/v1/expenses/abc", "", 400, "bad_request"},
		{"bad date", nil, http.MethodGet, "/api/v1/expenses?from=yesterday", "", 400, "bad_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := do(t, newTestHandler(&fakeExpenses{err: tt.err}, nil), tt.method, tt.path, tt.body)
			if status != tt.status || errorCode(body) != tt.code {
				t.Errorf("got %d %v, want %d %s", status, body, tt.status, tt.code)
			}
			if tt.status == 500 && strings.Contains(body["error"].(map[string]any)["message"].(string), "connection") {
				t.Error("internal error details leaked to the client")
			}
		})
	}
}

func TestHealthz(t *testing.T) {
	if status, _ := do(t, newTestHandler(&fakeExpenses{}, nil), http.MethodGet, "/healthz", ""); status != 200 {
		t.Errorf("healthy: status = %d", status)
	}
	if status, _ := do(t, newTestHandler(&fakeExpenses{}, errors.New("db down")), http.MethodGet, "/healthz", ""); status != 503 {
		t.Errorf("unhealthy: status = %d", status)
	}
}

func TestListUsersShowsMembership(t *testing.T) {
	status, body := do(t, newTestHandler(&fakeExpenses{}, nil), http.MethodGet, "/api/v1/users", "")
	items, _ := body["items"].([]any)
	if status != 200 || len(items) != 2 {
		t.Fatalf("status = %d body = %v", status, body)
	}
	anna, boris := items[0].(map[string]any), items[1].(map[string]any)
	if anna["is_head"] != true || boris["is_head"] != false {
		t.Errorf("users = %v", items)
	}
}

func TestPanelNeedsAFamilyHead(t *testing.T) {
	for name, viewer := range map[string]Viewer{
		"no admin configured": func(context.Context) (user.User, error) { return user.User{}, ErrNoViewer },
		"admin is a member":   func(context.Context) (user.User, error) { return user.User{TelegramID: 1, FamilyID: 10}, nil },
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeExpenses{}
			h := newTestHandlerFor(f, &fakeFamily{}, nil, viewer)
			for _, path := range []string{"/api/v1/expenses", "/api/v1/users", "/api/v1/invites"} {
				if status, body := do(t, h, http.MethodGet, path, ""); status != 403 || errorCode(body) != "forbidden" {
					t.Errorf("%s: %d %v", path, status, body)
				}
			}
			if f.lastScope != (ledger.Scope{}) {
				t.Error("the service was called without a viewer")
			}
		})
	}
}

func TestExpensesOnlyForFamilyMembers(t *testing.T) {
	f := &fakeExpenses{}
	h := newTestHandler(f, nil)
	if status, body := do(t, h, http.MethodPost, "/api/v1/expenses", `{"user_id":9,"amount":1,"category":"food"}`); status != 400 || errorCode(body) != "unknown_user" {
		t.Errorf("create for a stranger: %d %v", status, body)
	}
	if f.lastCreate.UserID != 0 {
		t.Error("an expense for a stranger reached the service")
	}
	if status, body := do(t, h, http.MethodPatch, "/api/v1/expenses/7", `{"user_id":9}`); status != 400 || errorCode(body) != "unknown_user" {
		t.Errorf("hand over to a stranger: %d %v", status, body)
	}
}

func TestInviteEndpoints(t *testing.T) {
	h := newTestHandler(&fakeExpenses{}, nil)
	status, body := do(t, h, http.MethodPost, "/api/v1/invites", "")
	if status != 201 || body["link"] != "https://t.me/FamilyBot?start=abc" || body["status"] != "active" {
		t.Errorf("create: %d %v", status, body)
	}
	status, body = do(t, h, http.MethodGet, "/api/v1/invites", "")
	items, _ := body["items"].([]any)
	if status != 200 || len(items) != 2 ||
		items[0].(map[string]any)["status"] != "active" || items[1].(map[string]any)["status"] != "expired" {
		t.Errorf("list: %d %v", status, body)
	}
	if _, ok := items[0].(map[string]any)["link"]; ok {
		t.Error("the list must not expose invite links")
	}
	if status, _ := do(t, h, http.MethodDelete, "/api/v1/invites/1", ""); status != 204 {
		t.Errorf("revoke: %d", status)
	}
}

func TestFamilyErrorMapping(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		method, path string
		status       int
		code         string
	}{
		{"remove head", family.ErrHeadRemoval, http.MethodDelete, "/api/v1/users/1", 409, "conflict"},
		{"remove unknown", user.ErrNotFound, http.MethodDelete, "/api/v1/users/9", 404, "not_found"},
		{"bot never started", family.ErrBotUnknown, http.MethodPost, "/api/v1/invites", 409, "conflict"},
		{"revoke used invite", family.ErrInviteNotFound, http.MethodDelete, "/api/v1/invites/3", 404, "not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandlerWithFamily(&fakeExpenses{}, &fakeFamily{err: tt.err}, nil)
			status, body := do(t, h, tt.method, tt.path, "")
			if status != tt.status || errorCode(body) != tt.code {
				t.Errorf("got %d %v, want %d %s", status, body, tt.status, tt.code)
			}
		})
	}
}

func TestUpdateUserWeeklyReport(t *testing.T) {
	status, body := do(t, newTestHandler(&fakeExpenses{}, nil), http.MethodPatch, "/api/v1/users/1", `{"weekly_report":false}`)
	if status != 200 || body["weekly_report"] != false || body["telegram_id"] != float64(1) {
		t.Errorf("status = %d body = %v", status, body)
	}
}

func TestTimeSeries(t *testing.T) {
	f := &fakeExpenses{}
	status, body := do(t, newTestHandler(f, nil), http.MethodGet, "/api/v1/expenses/timeseries?bucket=week&user_id=1", "")
	items, _ := body["items"].([]any)
	if status != 200 || body["bucket"] != "week" || len(items) != 2 {
		t.Fatalf("status = %d body = %v", status, body)
	}
	first := items[0].(map[string]any)
	if first["start"] != "2026-10-12" || first["sum"] != float64(300) || len(first["categories"].([]any)) != 1 {
		t.Errorf("first point = %v", first)
	}
	if *f.lastFilter.UserID != 1 {
		t.Errorf("filter = %+v", f.lastFilter)
	}
	if status, _ := do(t, newTestHandler(f, nil), http.MethodGet, "/api/v1/expenses/timeseries", ""); status != 400 {
		t.Errorf("missing bucket: status = %d", status)
	}
}

func TestExpensesAPIHidesIncomes(t *testing.T) {
	salary := sample
	salary.Category = ledger.CategorySalary
	f := &fakeExpenses{op: &salary}
	h := newTestHandler(f, nil)
	for _, req := range []struct{ method, body string }{
		{http.MethodGet, ""}, {http.MethodPatch, `{"amount":1}`}, {http.MethodDelete, ""},
	} {
		if status, _ := do(t, h, req.method, "/api/v1/expenses/7", req.body); status != 404 {
			t.Errorf("%s an income: status = %d, want 404", req.method, status)
		}
	}
	if f.lastPatch.Amount != nil {
		t.Error("an income was updated through the expenses API")
	}
	status, body := do(t, h, http.MethodPost, "/api/v1/expenses", `{"user_id":1,"amount":1,"category":"salary"}`)
	if status != 400 || body["error"].(map[string]any)["field"] != "category" {
		t.Errorf("create with an income category: %d %v", status, body)
	}
	do(t, h, http.MethodGet, "/api/v1/expenses", "")
	if f.lastFilter.Kind == nil || *f.lastFilter.Kind != ledger.KindExpense {
		t.Errorf("list filter = %+v, want expenses only", f.lastFilter)
	}
	if status, body := do(t, newTestHandler(&fakeExpenses{err: &ledger.ValidationError{Field: "date", Message: "must not be in the future"}}, nil),
		http.MethodPost, "/api/v1/expenses", `{"user_id":1,"amount":1,"category":"food"}`); status != 400 ||
		body["error"].(map[string]any)["field"] != "spent_at" {
		t.Errorf("the date field keeps its API name: %d %v", status, body)
	}
}
