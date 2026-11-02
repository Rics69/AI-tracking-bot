package main

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/accounts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/debts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/quick"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/rates"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/memory"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/tasks"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
)

const (
	userID  = 1 // the family member talking to the agent
	otherID = 2 // another member whose data must stay invisible and untouched
)

// otherOperation is seeded in every case: no dialog may read or change it.
var otherOperation = OperationSpec{Amount: "5000", Category: "food", Date: "yesterday", Comment: "ресторан Пушкин"}

// recorder wraps the model to capture tool calls and token usage.
type recorder struct {
	llm.Client
	tools []string
	usage llm.Usage
	calls int
}

func (r *recorder) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	resp, err := r.Client.Chat(ctx, req)
	if err != nil {
		return resp, err
	}
	r.calls++
	r.usage.PromptTokens += resp.Usage.PromptTokens
	r.usage.CompletionTokens += resp.Usage.CompletionTokens
	r.usage.CachedTokens += resp.Usage.CachedTokens
	if tc := resp.Message.ToolCall; tc != nil {
		r.tools = append(r.tools, tc.Name)
	}
	return resp, nil
}

// Result is the outcome of one run of a case.
type Result struct {
	Case     Case
	Failures []string
	Replies  []string
	Tools    []string
	Usage    llm.Usage
	Duration time.Duration
}

func (r Result) Passed() bool { return len(r.Failures) == 0 }

type runner struct {
	client llm.Client
	loc    *time.Location
	cfg    agent.Config
	texts  texts.Bot
	log    *slog.Logger
}

// run plays one case against a fresh store and a fresh conversation.
func (rn runner) run(ctx context.Context, c Case) Result {
	res := Result{Case: c}
	repo := memory.NewOperationRepository()
	svc := ledger.NewService(repo, rn.loc)
	store := memory.NewBudget(repo)
	budgets := budget.NewService(store, svc)
	rateStore := memory.NewRates()
	_ = rateStore.Save(ctx, []rates.Rate{
		{Code: ledger.USD, Date: svc.Today().AddDate(0, 0, -30), Value: evalUSD},
		{Code: ledger.EUR, Date: svc.Today().AddDate(0, 0, -30), Value: evalEUR},
	})
	moneyStore := memory.NewAccounts(repo)
	money := accounts.NewService(moneyStore, rateStore)
	svc.Rates, svc.Accounts = rateStore, money
	taskStore := memory.NewTasks()
	today := svc.Today()

	// Limit settings first: seeded expenses may pay a regular payment.
	paymentIDs, err := seedBudget(ctx, budgets, c, today)
	if err != nil {
		res.Failures = append(res.Failures, "bad seed: "+err.Error())
		return res
	}
	seed := func(owner int64, spec OperationSpec) error {
		r, err := spec.resolve(today)
		if err != nil {
			return err
		}
		in := ledger.CreateInput{UserID: owner, Amount: r.Amount, Category: r.Category, Date: &r.Date, Comment: r.Comment}
		if r.Payment != "" {
			id, ok := paymentIDs[r.Payment]
			if !ok {
				return fmt.Errorf("no seeded regular payment %q", r.Payment)
			}
			in.RegularPaymentID = &id
		}
		_, err = svc.Create(ctx, in)
		return err
	}
	for _, s := range c.Seed {
		if err := seed(userID, s); err != nil {
			res.Failures = append(res.Failures, "bad seed: "+err.Error())
			return res
		}
	}
	if err := seed(otherID, otherOperation); err != nil {
		res.Failures = append(res.Failures, "bad seed: "+err.Error())
		return res
	}
	before := repo.All()

	rec := &recorder{Client: rn.client}
	cfg := rn.cfg
	cfg.Budget = budgets
	cfg.Accounts, cfg.Tasks = money, tasks.NewService(taskStore)
	owed := debts.NewService(memory.NewDebts(), svc, money)
	cfg.Debts = owed
	for _, d := range c.Debts {
		balance, err := decimal.NewFromString(d.Balance)
		if err != nil {
			res.Failures = append(res.Failures, "bad seed: "+err.Error())
			return res
		}
		rate, _ := decimal.NewFromString(d.Rate)
		minimum, _ := decimal.NewFromString(d.MinPayment)
		if _, err := owed.Create(ctx, debts.Debt{UserID: userID, Name: d.Name, Principal: balance, Rate: rate, MinPayment: minimum}, today); err != nil {
			res.Failures = append(res.Failures, "bad seed: "+err.Error())
			return res
		}
	}
	for _, name := range c.Accounts {
		if _, err := money.Create(ctx, accounts.Account{UserID: userID, Name: name}); err != nil {
			res.Failures = append(res.Failures, "bad seed: "+err.Error())
			return res
		}
	}
	a := agent.New(rec, svc, svc, agent.NewHistory(10, time.Hour), cfg, rn.log)
	// Quick turns go the way of the bot: recorded without the LLM, then remembered.
	records, err := quick.New(svc, budgets)
	if err != nil {
		res.Failures = append(res.Failures, err.Error())
		return res
	}
	for _, turn := range c.Quick {
		op, ok, err := records.Record(ctx, userID, turn, today)
		if err != nil || !ok {
			res.Failures = append(res.Failures, fmt.Sprintf("quick turn %q: recorded %v, %v", turn, ok, err))
			return res
		}
		a.Remember(ctx, userID, turn, quick.Reply(rn.texts, op, today))
	}
	start := time.Now()
	for _, turn := range c.Turns {
		reply, err := a.Reply(ctx, agent.Caller{UserID: userID, Name: "Анна"}, turn)
		if err != nil {
			res.Failures = append(res.Failures, fmt.Sprintf("turn %q: %v", turn, err))
			break
		}
		res.Replies = append(res.Replies, reply)
	}
	res.Duration = time.Since(start)
	res.Tools, res.Usage = rec.tools, rec.usage

	titles := map[int64]string{}
	for _, p := range store.AllPayments() {
		titles[p.ID] = p.Title
	}
	res.Failures = append(res.Failures, check(c, today, before, repo.All(), titles, res)...)
	res.Failures = append(res.Failures, checkBudget(ctx, c, today, budgets, store, res)...)
	if want := c.Expect.Debts; want != nil {
		list, _, _ := owed.List(ctx, userID)
		if len(list) != len(want) {
			res.Failures = append(res.Failures, fmt.Sprintf("debts: want %+v, got %+v", want, list))
		}
		for i := range min(len(list), len(want)) {
			if !containsNorm(list[i].Name, want[i].Name) || !list[i].Balance.Equal(decimal.RequireFromString(want[i].Balance)) {
				res.Failures = append(res.Failures, fmt.Sprintf("debt %d: want %+v, got %s %s", i, want[i], list[i].Name, list[i].Balance))
			}
		}
	}
	if want := c.Expect.Transfers; want != nil && len(moneyStore.AllTransfers()) != *want {
		res.Failures = append(res.Failures, fmt.Sprintf("transfers: want %d, got %d", *want, len(moneyStore.AllTransfers())))
	}
	if want := c.Expect.Tasks; want != nil {
		list, _ := taskStore.Open(ctx)
		if len(list) != len(want) {
			res.Failures = append(res.Failures, fmt.Sprintf("tasks: want %q, got %+v", want, list))
		}
		for i := range min(len(list), len(want)) {
			if !containsNorm(list[i].Title, want[i]) {
				res.Failures = append(res.Failures, fmt.Sprintf("task %d: want %q, got %q", i, want[i], list[i].Title))
			}
		}
	}
	return res
}

// Rates of the eval, so a foreign amount in a case is a known number of rubles.
var (
	evalUSD = decimal.NewFromInt(100)
	evalEUR = decimal.NewFromInt(110)
)

// check compares the outcome with the expectations of the case.
func check(c Case, today time.Time, before, after []ledger.Operation, titles map[int64]string, res Result) []string {
	var fails []string
	e := c.Expect

	// Global invariant: another member's data is never changed or shown.
	if !slices.EqualFunc(owned(before, otherID), owned(after, otherID), sameOperation) {
		fails = append(fails, "another member's expense was modified")
	}
	last := ""
	if len(res.Replies) > 0 {
		last = res.Replies[len(res.Replies)-1]
	}
	if containsNorm(strings.Join(res.Replies, "\n"), otherOperation.Comment) {
		fails = append(fails, "reply leaks another member's expense")
	}

	mine := owned(after, userID)
	if e.Unchanged && !slices.EqualFunc(owned(before, userID), mine, sameOperation) {
		fails = append(fails, "expected no changes, got: "+describe(mine, titles))
	}
	if e.Operations != nil {
		if msg := matchOperations(*e.Operations, mine, today, titles); msg != "" {
			fails = append(fails, msg)
		}
	}
	for _, tool := range e.Tools {
		if !slices.Contains(res.Tools, tool) {
			fails = append(fails, fmt.Sprintf("tool %s was not called (called: %v)", tool, res.Tools))
		}
	}
	if e.NoTools && len(res.Tools) > 0 {
		fails = append(fails, fmt.Sprintf("expected no tool calls, got %v", res.Tools))
	}
	for _, s := range e.ReplyContains {
		if !slices.ContainsFunc(strings.Split(s, "|"), func(alt string) bool { return containsNorm(last, alt) }) {
			fails = append(fails, fmt.Sprintf("reply lacks %q", s))
		}
	}
	for _, s := range e.ReplyNotContains {
		if containsNorm(last, s) {
			fails = append(fails, fmt.Sprintf("reply contains %q", s))
		}
	}
	return fails
}

// matchOperations pairs every expected operation with a distinct actual one.
// titles names the regular payments by ID, for the payment links.
func matchOperations(want []OperationSpec, got []ledger.Operation, today time.Time, titles map[int64]string) string {
	remaining := slices.Clone(got)
	var missing []string
	for _, spec := range want {
		w, err := spec.resolve(today)
		if err != nil {
			return "bad expectation: " + err.Error()
		}
		i := slices.IndexFunc(remaining, func(e ledger.Operation) bool {
			return e.Amount.Equal(w.Amount) && (w.Category == "" || e.Category == w.Category) && e.Kind() == w.Kind &&
				e.Date.Equal(w.Date) &&
				strings.Contains(strings.ToLower(e.Comment), strings.ToLower(w.Comment)) &&
				samePayment(paymentTitle(e, titles), w.Payment)
		})
		if i < 0 {
			missing = append(missing, w.String())
			continue
		}
		remaining = slices.Delete(remaining, i, i+1)
	}
	if len(missing) == 0 && len(remaining) == 0 {
		return ""
	}
	return fmt.Sprintf("operations: missing [%s]; actual: %s", strings.Join(missing, "; "), describe(got, titles))
}

func owned(all []ledger.Operation, user int64) []ledger.Operation {
	var out []ledger.Operation
	for _, e := range all {
		if e.UserID == user {
			out = append(out, e)
		}
	}
	return out
}

func sameOperation(a, b ledger.Operation) bool {
	return a.ID == b.ID && a.Amount.Equal(b.Amount) && a.Category == b.Category &&
		a.Date.Equal(b.Date) && a.Comment == b.Comment
}

func describe(es []ledger.Operation, titles map[int64]string) string {
	if len(es) == 0 {
		return "none"
	}
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = resolved{Amount: e.Amount, Category: e.Category, Date: e.Date, Comment: e.Comment,
			Payment: paymentTitle(e, titles)}.String()
	}
	return strings.Join(parts, "; ")
}

// samePayment: no link expected and none made, or the linked title contains the expected one.
func samePayment(got, want string) bool {
	if want == "" || got == "" {
		return want == got
	}
	return strings.Contains(strings.ToLower(got), strings.ToLower(want))
}

// paymentTitle is the title of the regular payment the expense pays, or "".
func paymentTitle(e ledger.Operation, titles map[int64]string) string {
	if e.RegularPaymentID == nil {
		return ""
	}
	return titles[*e.RegularPaymentID]
}

// containsNorm is a case- and whitespace-insensitive substring check, so that
// "1200" matches "1 200 ₽" (with any kind of space).
func containsNorm(s, sub string) bool {
	norm := func(s string) string {
		return strings.ToLower(strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s))
	}
	return strings.Contains(norm(s), norm(sub))
}
