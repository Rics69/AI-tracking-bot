package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/initdata"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/memory"
)

const botToken = "123456:test-token"

const (
	anna  int64 = 1 // head of the family in most tests
	boris int64 = 2 // Anna's family member
	vera  int64 = 3 // a stranger
)

var names = map[int64]string{anna: "Анна", boris: "Борис", vera: "Вера"}

// env runs the real services on in-memory storage behind the HTTP handler.
type env struct {
	h       http.Handler
	users   *memory.Users
	ops     *memory.OperationRepository
	family  *family.Service
	auth    *Auth
	ledgerS *ledger.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	users := memory.NewUsers()
	ops := memory.NewOperationRepository()
	users.Expenses = ops
	settings := memory.NewSettings()
	_ = settings.Set(context.Background(), family.SettingBotUsername, "SavepointBot")
	fam := family.NewService(users, users, settings)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := &env{users: users, ops: ops, family: fam, ledgerS: ledger.NewService(ops, time.UTC)}
	e.auth = &Auth{BotToken: botToken, Users: fam, Log: log}
	e.h = NewHandler(Deps{
		Ledger: e.ledgerS, Family: fam, Auth: e.auth, Location: time.UTC,
		Health: func(context.Context) error { return nil }, Log: log,
	})
	return e
}

// initData signs init data for a user as Telegram does.
func initData(id int64) string {
	return initdata.Sign(initdata.User{ID: id, FirstName: names[id]}, time.Now(), botToken)
}

// do sends a request as user id (0: no Authorization header).
func (e *env) do(t *testing.T, id int64, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if id != 0 {
		req.Header.Set("Authorization", "tma "+initData(id))
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: response is not JSON: %q", method, path, rec.Body.String())
		}
	}
	return rec.Code, out
}

// ok sends a request and fails the test unless it gets want.
func (e *env) ok(t *testing.T, id int64, want int, method, path, body string) map[string]any {
	t.Helper()
	status, out := e.do(t, id, method, path, body)
	if status != want {
		t.Fatalf("%s %s as %d: status %d, want %d: %v", method, path, id, status, want, out)
	}
	return out
}

// onboard makes users accept the data policy through the API.
func (e *env) onboard(t *testing.T, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		e.ok(t, id, 200, http.MethodPatch, "/api/v1/me", `{"consent":true}`)
	}
}

// join puts member into head's family through an invite.
func (e *env) join(t *testing.T, head, member int64) {
	t.Helper()
	link := e.ok(t, head, 201, http.MethodPost, "/api/v1/family/invites", "")["link"].(string)
	_, code, _ := strings.Cut(link, "start=")
	if _, err := e.family.Join(context.Background(), code, member); err != nil {
		t.Fatal(err)
	}
}

func errorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestAuthentication(t *testing.T) {
	e := newEnv(t)
	for name, header := range map[string]string{
		"no header":      "",
		"another scheme": "Bearer " + initData(anna),
		"another bot":    "tma " + initdata.Sign(initdata.User{ID: anna, FirstName: "Анна"}, time.Now(), "999:other"),
		"expired":        "tma " + initdata.Sign(initdata.User{ID: anna, FirstName: "Анна"}, time.Now().Add(-25*time.Hour), botToken),
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != 401 || !strings.Contains(rec.Body.String(), `"unauthorized"`) {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// Valid init data: a new user appears, like after /start in the bot.
	me := e.ok(t, anna, 200, http.MethodGet, "/api/v1/me", "")
	if me["telegram_id"] != float64(anna) || me["first_name"] != "Анна" || me["is_head"] != true || me["consented"] != false {
		t.Errorf("me = %v", me)
	}
	if me["effective_timezone"] != "UTC" || me["timezone"] != nil {
		t.Errorf("timezone = %v / %v", me["timezone"], me["effective_timezone"])
	}
	// The spec, docs and health check are public.
	for _, path := range []string{"/api/v1/openapi.json", "/docs", "/healthz"} {
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 200 {
			t.Errorf("%s: status %d", path, rec.Code)
		}
	}
}

func TestDevLogin(t *testing.T) {
	e := newEnv(t)
	e.auth.DevUserID = anna
	me := e.ok(t, 0, 200, http.MethodGet, "/api/v1/me", "")
	if me["telegram_id"] != float64(anna) {
		t.Errorf("dev user = %v", me)
	}
	// Real init data still wins over the dev user.
	if me := e.ok(t, boris, 200, http.MethodGet, "/api/v1/me", ""); me["telegram_id"] != float64(boris) {
		t.Errorf("signed request = %v", me)
	}
}

func TestBlockedUserIsForbidden(t *testing.T) {
	e := newEnv(t)
	e.ok(t, anna, 200, http.MethodGet, "/api/v1/me", "")
	e.users.Block(anna)
	if status, body := e.do(t, anna, http.MethodGet, "/api/v1/me", ""); status != 403 || errorCode(body) != "forbidden" {
		t.Errorf("blocked: %d %v", status, body)
	}
}

func TestConsentComesFirst(t *testing.T) {
	e := newEnv(t)
	for _, req := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/operations", ""},
		{http.MethodPost, "/api/v1/operations", `{"amount":1,"category":"food"}`},
		{http.MethodGet, "/api/v1/family", ""},
		{http.MethodPatch, "/api/v1/me", `{"timezone":"Asia/Omsk"}`},
	} {
		if status, body := e.do(t, anna, req.method, req.path, req.body); status != 403 || errorCode(body) != "consent_required" {
			t.Errorf("%s %s before consent: %d %v", req.method, req.path, status, body)
		}
	}
	e.ok(t, anna, 200, http.MethodGet, "/api/v1/categories", "") // reference data is fine
	if status, body := e.do(t, anna, http.MethodPatch, "/api/v1/me", `{"consent":false}`); status != 400 || errorCode(body) != "validation_error" {
		t.Errorf("withdraw consent: %d %v", status, body)
	}
	me := e.ok(t, anna, 200, http.MethodPatch, "/api/v1/me", `{"consent":true,"timezone":"Asia/Omsk","weekly_report":false}`)
	if me["consented"] != true || me["timezone"] != "Asia/Omsk" || me["weekly_report"] != false {
		t.Errorf("me = %v", me)
	}
	if status, body := e.do(t, anna, http.MethodPatch, "/api/v1/me", `{"timezone":"Mars/Olympus"}`); status != 400 ||
		body["error"].(map[string]any)["field"] != "timezone" {
		t.Errorf("bad timezone: %d %v", status, body)
	}
}

func TestOperations(t *testing.T) {
	e := newEnv(t)
	e.onboard(t, anna)
	coffee := e.ok(t, anna, 201, http.MethodPost, "/api/v1/operations",
		`{"amount":350.5,"category":"food","date":"2026-10-14","comment":"кофе"}`)
	if coffee["kind"] != "expense" || coffee["amount"] != 350.5 || coffee["date"] != "2026-10-14" || coffee["user_id"] != float64(anna) {
		t.Errorf("created = %v", coffee)
	}
	salary := e.ok(t, anna, 201, http.MethodPost, "/api/v1/operations", `{"amount":80000,"category":"salary"}`)
	if salary["kind"] != "income" || salary["date"] != time.Now().UTC().Format(time.DateOnly) {
		t.Errorf("income without a date = %v", salary)
	}

	list := e.ok(t, anna, 200, http.MethodGet, "/api/v1/operations", "")
	if list["total"] != float64(2) || list["expense_sum"] != 350.5 || list["income_sum"] != float64(80000) {
		t.Errorf("list = %v", list)
	}
	list = e.ok(t, anna, 200, http.MethodGet, "/api/v1/operations?kind=income", "")
	if list["total"] != float64(1) {
		t.Errorf("incomes = %v", list)
	}

	id := int64(coffee["id"].(float64))
	path := fmt.Sprintf("/api/v1/operations/%d", id)
	updated := e.ok(t, anna, 200, http.MethodPatch, path, `{"amount":450,"category":"refund"}`)
	if updated["amount"] != float64(450) || updated["kind"] != "income" || updated["comment"] != "кофе" {
		t.Errorf("updated = %v", updated)
	}
	e.ok(t, anna, 204, http.MethodDelete, path, "")
	e.ok(t, anna, 404, http.MethodGet, path, "")

	if status, body := e.do(t, anna, http.MethodPost, "/api/v1/operations", `{"amount":0,"category":"food"}`); status != 400 ||
		body["error"].(map[string]any)["field"] != "amount" {
		t.Errorf("zero amount: %d %v", status, body)
	}
	if status, body := e.do(t, anna, http.MethodPost, "/api/v1/operations",
		fmt.Sprintf(`{"amount":1,"category":"food","date":"%s"}`, time.Now().AddDate(0, 0, 2).Format(time.DateOnly))); status != 400 ||
		body["error"].(map[string]any)["field"] != "date" {
		t.Errorf("future date: %d %v", status, body)
	}
}

func TestFamilySharesTheViewNotThePen(t *testing.T) {
	e := newEnv(t)
	e.onboard(t, anna, boris, vera)
	e.join(t, anna, boris)
	annas := e.ok(t, anna, 201, http.MethodPost, "/api/v1/operations", `{"amount":100,"category":"food"}`)
	boriss := e.ok(t, boris, 201, http.MethodPost, "/api/v1/operations", `{"amount":200,"category":"food"}`)
	e.ok(t, vera, 201, http.MethodPost, "/api/v1/operations", `{"amount":300,"category":"food"}`)

	// The head sees the family, a member only themselves, nobody sees Vera.
	if list := e.ok(t, anna, 200, http.MethodGet, "/api/v1/operations", ""); list["total"] != float64(2) || list["expense_sum"] != float64(300) {
		t.Errorf("head's list = %v", list)
	}
	if list := e.ok(t, anna, 200, http.MethodGet, "/api/v1/operations?user_id=2", ""); list["total"] != float64(1) {
		t.Errorf("head filters by member = %v", list)
	}
	if list := e.ok(t, boris, 200, http.MethodGet, "/api/v1/operations", ""); list["total"] != float64(1) {
		t.Errorf("member's list = %v", list)
	}
	if list := e.ok(t, boris, 200, http.MethodGet, "/api/v1/operations?user_id=1", ""); list["total"] != float64(0) {
		t.Errorf("member asks for the head's operations = %v", list)
	}

	borisPath := fmt.Sprintf("/api/v1/operations/%d", int64(boriss["id"].(float64)))
	e.ok(t, anna, 200, http.MethodGet, borisPath, "")
	// ...but only reads them.
	e.ok(t, anna, 404, http.MethodPatch, borisPath, `{"amount":1}`)
	e.ok(t, anna, 404, http.MethodDelete, borisPath, "")
	e.ok(t, boris, 404, http.MethodGet, fmt.Sprintf("/api/v1/operations/%d", int64(annas["id"].(float64))), "")

	sum := e.ok(t, anna, 200, http.MethodGet, "/api/v1/summary", "")
	if sum["expense"].(map[string]any)["sum"] != float64(300) || sum["balance"] != float64(-300) {
		t.Errorf("head's summary = %v", sum)
	}
}

func TestSummaryTimeSeriesAndSavik(t *testing.T) {
	e := newEnv(t)
	e.onboard(t, anna)
	today := time.Now().UTC().Format(time.DateOnly)
	e.ok(t, anna, 201, http.MethodPost, "/api/v1/operations", `{"amount":1000,"category":"salary"}`)
	e.ok(t, anna, 201, http.MethodPost, "/api/v1/operations", `{"amount":300,"category":"food"}`)

	sum := e.ok(t, anna, 200, http.MethodGet, "/api/v1/summary?from="+today, "")
	income := sum["income"].(map[string]any)
	if income["sum"] != float64(1000) || income["count"] != float64(1) || sum["balance"] != float64(700) ||
		len(sum["expense"].(map[string]any)["categories"].([]any)) != 1 {
		t.Errorf("summary = %v", sum)
	}

	series := e.ok(t, anna, 200, http.MethodGet, "/api/v1/timeseries?bucket=day&kind=income&from="+today, "")
	items := series["items"].([]any)
	if series["kind"] != "income" || len(items) != 1 || items[0].(map[string]any)["sum"] != float64(1000) {
		t.Errorf("series = %v", series)
	}
	if status, _ := e.do(t, anna, http.MethodGet, "/api/v1/timeseries?bucket=day", ""); status != 400 {
		t.Errorf("series without kind: %d", status)
	}

	s := e.ok(t, anna, 200, http.MethodGet, "/api/v1/savik", "")
	if s["state"] != "neutral" || s["month"].(map[string]any)["balance"] != float64(700) {
		t.Errorf("savik = %v", s)
	}
	e.ok(t, anna, 201, http.MethodPost, "/api/v1/operations", `{"amount":2000,"category":"housing"}`)
	if s := e.ok(t, anna, 200, http.MethodGet, "/api/v1/savik", ""); s["state"] != "empty" {
		t.Errorf("spent more than earned: savik = %v", s)
	}
}

func TestFamilyEndpoints(t *testing.T) {
	e := newEnv(t)
	e.onboard(t, anna, boris, vera)
	e.join(t, anna, boris)

	fam := e.ok(t, anna, 200, http.MethodGet, "/api/v1/family", "")
	members, invites := fam["members"].([]any), fam["invites"].([]any)
	if len(members) != 2 || members[0].(map[string]any)["telegram_id"] != float64(anna) || len(invites) != 1 ||
		invites[0].(map[string]any)["status"] != "used" {
		t.Errorf("head's family = %v", fam)
	}
	if _, ok := invites[0].(map[string]any)["link"]; ok {
		t.Error("the list must not expose invite links")
	}
	if fam := e.ok(t, boris, 200, http.MethodGet, "/api/v1/family", ""); len(fam["invites"].([]any)) != 0 {
		t.Errorf("a member sees invites: %v", fam)
	}

	// Members cannot manage the family.
	for _, req := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/family/invites", ""},
		{http.MethodDelete, "/api/v1/family/invites/1", ""},
		{http.MethodDelete, "/api/v1/family/members/1", ""},
		{http.MethodPut, "/api/v1/family/head", `{"telegram_id":2}`},
		{http.MethodPatch, "/api/v1/family/members/1", `{"weekly_report":false}`},
	} {
		if status, body := e.do(t, boris, req.method, req.path, req.body); status != 403 || errorCode(body) != "forbidden" {
			t.Errorf("member: %s %s = %d %v", req.method, req.path, status, body)
		}
	}
	if m := e.ok(t, boris, 200, http.MethodPatch, "/api/v1/family/members/2", `{"weekly_report":false}`); m["weekly_report"] != false {
		t.Errorf("own setting = %v", m)
	}
	e.ok(t, anna, 404, http.MethodPatch, "/api/v1/family/members/3", `{"weekly_report":false}`) // a stranger
	e.ok(t, anna, 409, http.MethodDelete, "/api/v1/family/members/1", "")                       // the head themself
	e.ok(t, anna, 409, http.MethodPost, "/api/v1/family/leave", "")                             // head with members

	// Hand over the head role, then the former head leaves.
	e.ok(t, anna, 204, http.MethodPut, "/api/v1/family/head", `{"telegram_id":2}`)
	if me := e.ok(t, boris, 200, http.MethodGet, "/api/v1/me", ""); me["is_head"] != true {
		t.Errorf("new head = %v", me)
	}
	e.ok(t, anna, 204, http.MethodPost, "/api/v1/family/leave", "")
	e.ok(t, boris, 409, http.MethodPost, "/api/v1/family/leave", "") // alone now

	// Remove a member.
	e.join(t, boris, vera)
	e.ok(t, boris, 204, http.MethodDelete, "/api/v1/family/members/3", "")
	if fam := e.ok(t, boris, 200, http.MethodGet, "/api/v1/family", ""); len(fam["members"].([]any)) != 1 {
		t.Errorf("after removal = %v", fam)
	}
}

func TestDeleteMe(t *testing.T) {
	e := newEnv(t)
	e.onboard(t, anna)
	e.ok(t, anna, 201, http.MethodPost, "/api/v1/operations", `{"amount":1,"category":"food"}`)
	e.ok(t, anna, 204, http.MethodDelete, "/api/v1/me", "")
	if len(e.ops.All()) != 0 {
		t.Errorf("operations left: %v", e.ops.All())
	}
	// Opening the app again starts from scratch.
	if me := e.ok(t, anna, 200, http.MethodGet, "/api/v1/me", ""); me["consented"] != false {
		t.Errorf("after deletion = %v", me)
	}
}

// failingLedger turns every call into an infrastructure error.
type failingLedger struct{ *ledger.Service }

func (failingLedger) List(context.Context, ledger.Scope, ledger.Filter) (ledger.Page, error) {
	return ledger.Page{}, errors.New("connection refused to 10.0.0.5")
}

func TestErrors(t *testing.T) {
	e := newEnv(t)
	e.onboard(t, anna)
	for _, tt := range []struct {
		name, method, path, body, code string
		status                         int
	}{
		{"malformed json", http.MethodPost, "/api/v1/operations", `{"amount":`, "bad_request", 400},
		{"bad id", http.MethodGet, "/api/v1/operations/abc", "", "bad_request", 400},
		{"bad date", http.MethodGet, "/api/v1/operations?from=yesterday", "", "bad_request", 400},
		{"unknown category", http.MethodPost, "/api/v1/operations", `{"amount":1,"category":"crypto"}`, "validation_error", 400},
		{"not found", http.MethodDelete, "/api/v1/operations/999", "", "not_found", 404},
	} {
		if status, body := e.do(t, anna, tt.method, tt.path, tt.body); status != tt.status || errorCode(body) != tt.code {
			t.Errorf("%s: %d %v, want %d %s", tt.name, status, body, tt.status, tt.code)
		}
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewHandler(Deps{
		Ledger: failingLedger{e.ledgerS}, Family: e.family, Auth: e.auth, Location: time.UTC,
		Health: func(context.Context) error { return errors.New("db down") }, Log: log,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/operations", nil)
	req.Header.Set("Authorization", "tma "+initData(anna))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Errorf("internal error: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != 503 {
		t.Errorf("unhealthy: %d", rec.Code)
	}
}
