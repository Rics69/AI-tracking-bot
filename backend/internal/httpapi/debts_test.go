package httpapi

import (
	"fmt"
	"net/http"
	"testing"
)

func TestDebtEndpoints(t *testing.T) {
	e := newEnv(t)
	e.onboard(t, anna, boris)
	if status, body := e.do(t, vera, http.MethodGet, "/api/v1/debts", ""); status != 403 || errorCode(body) != "consent_required" {
		t.Errorf("debts before consent: %d %v", status, body)
	}

	card := e.ok(t, anna, 201, http.MethodPost, "/api/v1/debts", `{"name":"Карта","kind":"card","balance":150000,"rate":29.9,"min_payment":4500,"due_day":10}`)
	e.ok(t, anna, 201, http.MethodPost, "/api/v1/debts", `{"name":"Кредит","kind":"loan","balance":300000,"rate":17.5,"min_payment":9000}`)
	e.ok(t, anna, 201, http.MethodPost, "/api/v1/debts", `{"name":"Рассрочка","kind":"installment","balance":40000,"min_payment":5000}`)
	if card["rate"] != 29.9 {
		t.Errorf("card = %v", card)
	}

	// The sum over the minimums must reach the plan: the reference from the
	// debts package says 21 months with it.
	months := func(path string) float64 {
		t.Helper()
		plan := e.ok(t, anna, 200, http.MethodGet, path, "")
		return plan["avalanche"].(map[string]any)["months"].(float64)
	}
	plain, extra := months("/api/v1/debts/plan"), months("/api/v1/debts/plan?extra=10000")
	if extra != 21 || plain <= extra {
		t.Errorf("months: %v without extra, %v with it", plain, extra)
	}
	if status, _ := e.do(t, anna, http.MethodGet, "/api/v1/debts/plan?extra=abc", ""); status != 400 {
		t.Errorf("a broken sum: %d", status)
	}

	id := int64(card["id"].(float64))
	path := fmt.Sprintf("/api/v1/debts/%d", id)
	if status, _ := e.do(t, boris, http.MethodPost, path+"/payments", `{"amount":1000}`); status != 404 {
		t.Errorf("paid someone else's debt: %d", status)
	}
	paid := e.ok(t, anna, 201, http.MethodPost, path+"/payments", `{"amount":5000}`)
	if paid["balance"] != 145000.0 {
		t.Errorf("paid = %v", paid)
	}
	if len(e.ops.All()) != 1 {
		t.Errorf("a payment is an expense: %+v", e.ops.All())
	}
	worth := e.ok(t, anna, 200, http.MethodGet, "/api/v1/net-worth", "")
	if worth["debts"] != 485000.0 || worth["assets"] != -5000.0 || worth["net"] != -490000.0 {
		t.Errorf("net worth = %v", worth)
	}

	list := e.ok(t, anna, 200, http.MethodGet, path+"/payments", "")["items"].([]any)
	if len(list) != 1 {
		t.Fatalf("payments = %v", list)
	}
	pay := fmt.Sprintf("/api/v1/debt-payments/%d", int64(list[0].(map[string]any)["id"].(float64)))
	if status, _ := e.do(t, boris, http.MethodDelete, pay, ""); status != 404 {
		t.Errorf("cancelled someone else's payment: %d", status)
	}
	if status, _ := e.do(t, anna, http.MethodDelete, pay, ""); status != 204 {
		t.Errorf("cancel: %d", status)
	}
	if got := e.ok(t, anna, 200, http.MethodGet, "/api/v1/debts", ""); got["total"] != 490000.0 {
		t.Errorf("after cancelling = %v", got)
	}

	fixed := e.ok(t, anna, 200, http.MethodPatch, path, `{"balance":151200}`)
	if fixed["balance"] != 151200.0 {
		t.Errorf("corrected = %v", fixed)
	}
	if status, _ := e.do(t, anna, http.MethodDelete, path, ""); status != 204 {
		t.Errorf("delete: %d", status)
	}
	if status, body := e.do(t, anna, http.MethodGet, "/api/v1/debts/plan", ""); status != 200 {
		t.Errorf("plan: %d %v", status, body)
	}
}

func TestDebtPlanThatNeverEnds(t *testing.T) {
	e := newEnv(t)
	e.onboard(t, anna)
	e.ok(t, anna, 201, http.MethodPost, "/api/v1/debts", `{"name":"МФО","kind":"microloan","balance":100000,"rate":292,"min_payment":1000}`)
	status, body := e.do(t, anna, http.MethodGet, "/api/v1/debts/plan", "")
	if status != 400 {
		t.Errorf("plan = %d %v", status, body)
	}
}
