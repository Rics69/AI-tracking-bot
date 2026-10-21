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

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/expense"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// fakeExpenses records calls and returns canned results.
type fakeExpenses struct {
	err        error
	lastScope  expense.Scope
	lastFilter expense.Filter
	lastCreate expense.CreateInput
	lastPatch  expense.Patch
}

var sample = expense.Expense{
	ID: 7, UserID: 111, Amount: decimal.RequireFromString("350.50"), Category: expense.CategoryFood,
	SpentAt: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), Comment: "кофе",
}

func (f *fakeExpenses) Create(_ context.Context, in expense.CreateInput) (expense.Expense, error) {
	f.lastCreate = in
	return sample, f.err
}

func (f *fakeExpenses) Get(_ context.Context, s expense.Scope, _ int64) (expense.Expense, error) {
	f.lastScope = s
	return sample, f.err
}

func (f *fakeExpenses) Update(_ context.Context, s expense.Scope, _ int64, p expense.Patch) (expense.Expense, error) {
	f.lastScope, f.lastPatch = s, p
	return sample, f.err
}

func (f *fakeExpenses) Delete(_ context.Context, s expense.Scope, _ int64) error {
	f.lastScope = s
	return f.err
}

func (f *fakeExpenses) List(_ context.Context, s expense.Scope, flt expense.Filter) (expense.Page, error) {
	f.lastScope, f.lastFilter = s, flt
	return expense.Page{Items: []expense.Expense{sample}, Total: 1, Sum: sample.Amount}, f.err
}

func (f *fakeExpenses) TimeSeries(_ context.Context, s expense.Scope, flt expense.Filter, b expense.Bucket) ([]expense.Point, error) {
	f.lastScope, f.lastFilter = s, flt
	return []expense.Point{
		{Start: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), Sum: decimal.NewFromInt(300), Count: 1,
			Categories: []expense.CategoryTotal{{Category: expense.CategoryFood, Sum: decimal.NewFromInt(300), Count: 1}}},
		{Start: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC), Sum: decimal.Zero, Categories: []expense.CategoryTotal{}},
	}, f.err
}

func (f *fakeExpenses) Summary(_ context.Context, s expense.Scope, flt expense.Filter) (expense.Summary, error) {
	f.lastScope, f.lastFilter = s, flt
	return expense.Summary{}, f.err
}

// fakeFamily returns canned members and invites, or err from every call.
type fakeFamily struct {
	err     error
	removed []int64
}

func (f *fakeFamily) Members(context.Context) ([]user.User, error) {
	removedAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	return []user.User{
		{TelegramID: 1, FirstName: "Анна", IsHead: true},
		{TelegramID: 2, FirstName: "Борис", RemovedAt: &removedAt},
	}, f.err
}

func (f *fakeFamily) Remove(_ context.Context, id int64) error {
	f.removed = append(f.removed, id)
	return f.err
}

func (f *fakeFamily) CreateInvite(context.Context) (family.CreatedInvite, error) {
	return family.CreatedInvite{
		Invite: family.Invite{ID: 5, ExpiresAt: time.Now().Add(family.InviteTTL)},
		Link:   "https://t.me/FamilyBot?start=abc",
	}, f.err
}

func (f *fakeFamily) ListInvites(context.Context) ([]family.Invite, error) {
	past := time.Now().Add(-time.Hour)
	return []family.Invite{
		{ID: 1, ExpiresAt: time.Now().Add(time.Hour)},
		{ID: 2, ExpiresAt: past},
	}, f.err
}

func (f *fakeFamily) RevokeInvite(context.Context, int64) error { return f.err }

func (f *fakeFamily) SetWeeklyReport(_ context.Context, id int64, on bool) (user.User, error) {
	return user.User{TelegramID: id, FirstName: "Анна", WeeklyReport: on}, f.err
}
func (f *fakeFamily) Now() time.Time { return time.Now() }

func newTestHandler(f *fakeExpenses, health error) http.Handler {
	return newTestHandlerWithFamily(f, &fakeFamily{}, health)
}

func newTestHandlerWithFamily(f *fakeExpenses, fam *fakeFamily, health error) http.Handler {
	return NewHandler(Deps{
		Expenses: f,
		Family:   fam,
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
	if in.UserID != 111 || !in.Amount.Equal(decimal.RequireFromString("350.5")) || in.Category != expense.CategoryFood ||
		in.SpentAt == nil || in.SpentAt.Format(time.DateOnly) != "2026-10-14" || in.Comment != "кофе" {
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
	if *flt.UserID != 111 || *flt.Category != expense.CategoryFood ||
		flt.From.Format(time.DateOnly) != "2026-10-01" || flt.To.Format(time.DateOnly) != "2026-10-31" ||
		flt.Query != "кофе" || flt.Limit != 5 || flt.Offset != 10 {
		t.Errorf("filter = %+v", flt)
	}
	if !f.lastScope.All() {
		t.Error("panel must work in the AllUsers scope")
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
		p.Category != nil || p.Comment != nil || p.SpentAt != nil || p.UserID != nil {
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
		{"validation", &expense.ValidationError{Field: "amount", Message: "must be greater than zero"},
			http.MethodPost, "/api/v1/expenses", `{"user_id":1,"amount":0,"category":"food"}`, 400, "validation_error"},
		{"not found", expense.ErrNotFound, http.MethodGet, "/api/v1/expenses/1", "", 404, "not_found"},
		{"delete not found", expense.ErrNotFound, http.MethodDelete, "/api/v1/expenses/1", "", 404, "not_found"},
		{"unknown user", expense.ErrUnknownUser,
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
	if anna["is_head"] != true || anna["removed_at"] != nil || boris["removed_at"] != "2026-10-01T12:00:00Z" {
		t.Errorf("users = %v", items)
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
