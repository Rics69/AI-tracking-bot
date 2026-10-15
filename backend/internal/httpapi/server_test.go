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

func (f *fakeExpenses) Summary(_ context.Context, s expense.Scope, flt expense.Filter) (expense.Summary, error) {
	f.lastScope, f.lastFilter = s, flt
	return expense.Summary{}, f.err
}

type fakeUsers struct{}

func (fakeUsers) List(context.Context) ([]user.User, error) { return nil, nil }

func newTestHandler(f *fakeExpenses, health error) http.Handler {
	return NewHandler(Deps{
		Expenses: f,
		Users:    fakeUsers{},
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
