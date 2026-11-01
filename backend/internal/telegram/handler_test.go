package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/flow"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/quick"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/report"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/stats"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/memory"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

const (
	anna  int64 = 1
	boris int64 = 2
	admin int64 = 99
)

var names = map[int64]string{anna: "Анна", boris: "Борис", admin: "Админ"}

// out is a message the bot sent or a message it edited after a button press.
type out struct {
	chat    int64
	text    string
	buttons []string // callback data
	edit    bool
}

type fakeSender struct {
	mu       sync.Mutex
	out      []out
	failures int // the first N sends fail with a network error
	attempts int

	forbidden bool // the user blocked the bot
}

func (f *fakeSender) SendMessage(_ context.Context, p *bot.SendMessageParams) (*models.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.forbidden {
		return nil, fmt.Errorf("%w: bot was blocked by the user", bot.ErrorForbidden)
	}
	if f.attempts <= f.failures {
		return nil, errors.New("unexpected EOF")
	}
	f.out = append(f.out, out{chat: p.ChatID.(int64), text: p.Text, buttons: callbacks(p.ReplyMarkup)})
	return &models.Message{}, nil
}

func (f *fakeSender) EditMessageText(_ context.Context, p *bot.EditMessageTextParams) (*models.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.out = append(f.out, out{chat: p.ChatID.(int64), text: p.Text, buttons: callbacks(p.ReplyMarkup), edit: true})
	return &models.Message{}, nil
}

func (f *fakeSender) SendChatAction(context.Context, *bot.SendChatActionParams) (bool, error) {
	return true, nil
}

func (f *fakeSender) AnswerCallbackQuery(context.Context, *bot.AnswerCallbackQueryParams) (bool, error) {
	return true, nil
}

func callbacks(m models.ReplyMarkup) []string {
	kb, ok := m.(*models.InlineKeyboardMarkup)
	if !ok {
		return nil
	}
	var data []string
	for _, r := range kb.InlineKeyboard {
		for _, b := range r {
			if b.WebApp != nil {
				data = append(data, "webapp:"+b.WebApp.URL)
				continue
			}
			data = append(data, b.CallbackData)
		}
	}
	return data
}

// last returns the last message to chat.
func (f *fakeSender) last(t *testing.T, chat int64) out {
	t.Helper()
	for i := len(f.out) - 1; i >= 0; i-- {
		if f.out[i].chat == chat {
			return f.out[i]
		}
	}
	t.Fatalf("nothing sent to %d", chat)
	return out{}
}

type fakeAgent struct {
	reply   string
	err     error
	callers []agent.Caller
	resets  []int64

	remembered []string
}

func (f *fakeAgent) Reply(_ context.Context, c agent.Caller, _ string) (string, error) {
	f.callers = append(f.callers, c)
	return f.reply, f.err
}

func (f *fakeAgent) Reset(_ context.Context, id int64) { f.resets = append(f.resets, id) }

func (f *fakeAgent) Remember(_ context.Context, _ int64, text, reply string) {
	f.remembered = append(f.remembered, text+" → "+reply)
}

type fakeMeter struct {
	allow     bool
	exhausted bool
}

func (f *fakeMeter) Allow(context.Context, int64, time.Time) (bool, error) { return f.allow, nil }

func (f *fakeMeter) Exhausted(context.Context, time.Time) (bool, error) { return f.exhausted, nil }

type env struct {
	h      *Handler
	agent  *fakeAgent
	meter  *fakeMeter
	users  *memory.Users
	family *family.Service
	s      *fakeSender
	t      texts.Bot
	events *memory.Events
}

// tracked returns the events written so far.
func (e *env) tracked(name string) []memory.Event {
	e.h.cfg.Events.Wait()
	return e.events.Named(name)
}

func setup(t *testing.T) *env {
	t.Helper()
	tx, err := texts.Load()
	if err != nil {
		t.Fatal(err)
	}
	users := memory.NewUsers()
	settings := memory.NewSettings()
	_ = settings.Set(context.Background(), family.SettingBotUsername, "SavepointBot")
	fam := family.NewService(users, users, settings)
	e := &env{agent: &fakeAgent{reply: "Записано"}, meter: &fakeMeter{allow: true}, users: users, family: fam, s: &fakeSender{}, t: tx,
		events: memory.NewEvents()}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e.h = NewHandler(e.agent, fam, e.meter, Config{
		ReplyTimeout: time.Minute, Location: time.UTC, AdminID: admin, Texts: tx,
		Events: events.NewTracker(e.events, log),
	}, log)
	e.h.retryDelay = time.Millisecond
	return e
}

func (e *env) message(from int64, text string) {
	e.h.handle(context.Background(), e.s, &models.Update{Message: &models.Message{
		From: &models.User{ID: from, FirstName: names[from], Username: strings.ToLower(names[from])},
		Chat: models.Chat{ID: from, Type: models.ChatTypePrivate},
		Text: text,
	}})
}

func (e *env) press(from int64, data string) {
	e.h.handle(context.Background(), e.s, &models.Update{CallbackQuery: &models.CallbackQuery{
		ID:      "cb",
		From:    models.User{ID: from, FirstName: names[from]},
		Data:    data,
		Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: 5, Chat: models.Chat{ID: from}}},
	}})
}

// onboard makes the user accept the data policy.
func (e *env) onboard(t *testing.T, id int64) {
	t.Helper()
	e.message(id, "/start")
	e.press(id, cbConsent)
	if u, _ := e.users.Get(context.Background(), id); !u.Consented() {
		t.Fatalf("user %d did not consent", id)
	}
}

func TestNewUserConsentsBeforeAnythingElse(t *testing.T) {
	e := setup(t)
	e.message(anna, "кофе 350")
	if len(e.agent.callers) != 0 {
		t.Fatal("a message reached the agent before consent")
	}
	got := e.s.last(t, anna)
	if !strings.Contains(got.text, "GigaChat") || !slices.Equal(got.buttons, []string{cbConsent}) {
		t.Errorf("first reply = %+v, want the consent prompt with a button", got)
	}
	e.message(anna, "/privacy") // the prompt points there
	if got := e.s.last(t, anna); got.text != e.t.Privacy {
		t.Errorf("privacy before consent = %q", got.text)
	}

	e.press(anna, cbConsent)
	if got := e.s.last(t, anna); !got.edit || got.text != e.t.Welcome {
		t.Errorf("after consent = %+v, want the welcome in place of the prompt", got)
	}
	e.message(anna, "кофе 350")
	if len(e.agent.callers) != 1 || e.agent.callers[0].UserID != anna || e.agent.callers[0].Location != time.UTC {
		t.Errorf("agent callers = %+v", e.agent.callers)
	}
	if got := e.s.last(t, anna); got.text != "Записано" {
		t.Errorf("reply = %q", got.text)
	}
}

func TestJoinFamilyByInviteLink(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.onboard(t, anna)
	e.message(anna, "/family")
	if got := e.s.last(t, anna); got.text != e.t.FamilyAlone || !slices.Equal(got.buttons, []string{cbInvite}) {
		t.Fatalf("/family = %+v", got)
	}
	e.press(anna, cbInvite)
	link := e.s.last(t, anna).text
	_, code, ok := strings.Cut(link, "https://t.me/SavepointBot?start=")
	if !ok {
		t.Fatalf("invite message = %q", link)
	}
	code = strings.Fields(code)[0]

	// A new user opens the link: consent first, then the invitation.
	e.message(boris, "/start "+code)
	if got := e.s.last(t, boris); !slices.Equal(got.buttons, []string{cbConsent}) {
		t.Fatalf("new user with an invite = %+v, want consent", got)
	}
	e.press(boris, cbConsent)
	invitation := e.s.last(t, boris)
	if !strings.Contains(invitation.text, "Анна приглашает") || invitation.buttons[0] != cbJoin+":"+code {
		t.Fatalf("invitation = %+v", invitation)
	}
	e.press(boris, cbJoin+":"+code)
	if got := e.s.last(t, boris); got.text != texts.Fill(e.t.Joined, "head", "Анна") {
		t.Errorf("joined = %q", got.text)
	}
	if got := e.s.last(t, anna); got.text != texts.Fill(e.t.MemberJoined, "name", "Борис") {
		t.Errorf("head notified = %q", got.text)
	}
	a, _ := e.users.Get(ctx, anna)
	b, _ := e.users.Get(ctx, boris)
	if a.FamilyID != b.FamilyID || b.IsHead {
		t.Errorf("anna = %+v, boris = %+v", a, b)
	}

	// The link is single-use.
	e.message(boris, "/start "+code)
	if got := e.s.last(t, boris); got.text != e.t.InviteInvalid {
		t.Errorf("reused link = %q", got.text)
	}
}

func TestConsentMessageSentBeforeTheUpdateStillCarriesTheCode(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.onboard(t, anna)
	head, _ := e.users.Get(ctx, anna)
	inv, err := e.family.CreateInvite(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	_, code, _ := strings.Cut(inv.Link, "start=")
	e.message(boris, "кофе 350")
	e.press(boris, cbConsent+":"+code)
	if got := e.s.last(t, boris); !strings.Contains(got.text, "Анна приглашает") {
		t.Errorf("after an old consent button = %+v", got)
	}
}

func TestLinkBeforeConsentKeepsItsLabel(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	long := "go_dream__" + strings.Repeat("r", 54) // does not fit into a button with the prefix
	e.message(anna, "/start "+long)
	got := e.s.last(t, anna)
	if !slices.Equal(got.buttons, []string{cbConsent}) {
		t.Fatalf("consent = %+v", got)
	}
	if u, _ := e.users.Get(ctx, anna); u.PendingStart != long || u.FirstSource != "" {
		t.Fatalf("before consent = %+v", u)
	}
	start := e.tracked(events.Start)
	if len(start) != 1 || start[0].UserID != 0 || start[0].Props["feature"] != "dream" {
		t.Fatalf("start before consent must name nobody: %+v", start)
	}

	e.press(anna, cbConsent)
	if got := e.s.last(t, anna); got.text != e.t.Welcome {
		t.Errorf("after consent = %q", got.text)
	}
	u, _ := e.users.Get(ctx, anna)
	if u.PendingStart != "" || u.FirstSource != strings.Repeat("r", 54) || u.LastSource != u.FirstSource {
		t.Errorf("after consent = %+v", u)
	}
	consented := e.tracked(events.Consented)
	if len(consented) != 1 || consented[0].UserID != anna || consented[0].Props["source"] != u.FirstSource {
		t.Errorf("consented = %+v", consented)
	}
}

func TestSources(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.message(anna, "/start")
	e.press(anna, cbConsent)
	if u, _ := e.users.Get(ctx, anna); u.FirstSource != "direct" || u.LastSource != "direct" {
		t.Fatalf("no label = %+v", u)
	}
	e.message(anna, "/start go_goal__tg")
	if got := e.s.last(t, anna); got.text != e.t.Welcome {
		t.Errorf("a feature link = %q, want the welcome for now", got.text)
	}
	if u, _ := e.users.Get(ctx, anna); u.FirstSource != "direct" || u.LastSource != "tg" {
		t.Errorf("after a label = %+v", u)
	}
	e.message(anna, "/start")
	if u, _ := e.users.Get(ctx, anna); u.LastSource != "tg" {
		t.Errorf("a plain /start erased the source: %+v", u)
	}
	e.message(anna, "/start ch_noshopping")
	if got := e.s.last(t, anna); got.text != e.t.Welcome {
		t.Errorf("a challenge link = %q, want the welcome for now", got.text)
	}
	e.message(anna, "/start ???")
	if got := e.s.last(t, anna); got.text != e.t.Welcome {
		t.Errorf("a broken link = %q", got.text)
	}
}

// liveCode is a family where one code, whatever it looks like, is a live invite.
type liveCode struct {
	Family
	code string
	head user.User
}

func (f liveCode) Invitation(ctx context.Context, code string) (user.User, error) {
	if code == f.code {
		return f.head, nil
	}
	return f.Family.Invitation(ctx, code)
}

func TestInviteCodeThatLooksLikeALabel(t *testing.T) {
	for _, code := range []string{"go_dream__x", "go_0aB-cD", "ch_0aB-cD", "ref_0aB-cD"} {
		t.Run(code, func(t *testing.T) {
			e := setup(t)
			ctx := context.Background()
			e.onboard(t, anna)
			head, _ := e.users.Get(ctx, anna)
			e.h.family = liveCode{Family: e.family, code: code, head: head}
			e.onboard(t, boris)
			e.message(boris, "/start "+code)
			if got := e.s.last(t, boris); !strings.Contains(got.text, "Анна приглашает") {
				t.Errorf("a live invite = %+v", got)
			}
			if u, _ := e.users.Get(ctx, boris); u.LastSource != "invite" {
				t.Errorf("source = %+v", u)
			}
		})
	}
}

func TestPolicyAcceptedInTheAppKeepsTheLink(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.message(anna, "/start go_limit__yt")
	if _, err := e.family.Consent(ctx, anna); err != nil { // PATCH /me
		t.Fatal(err)
	}
	e.message(anna, "/start")
	if u, _ := e.users.Get(ctx, anna); u.PendingStart != "" || u.FirstSource != "yt" {
		t.Errorf("after /start = %+v", u)
	}
}

// setupQuick is setup with the fast path on.
func setupQuick(t *testing.T) (*env, *budget.Service) {
	t.Helper()
	e := setup(t)
	repo := memory.NewOperationRepository()
	svc := ledger.NewService(repo, time.UTC)
	budgets := budget.NewService(memory.NewBudget(repo), svc)
	q, err := quick.New(svc, budgets)
	if err != nil {
		t.Fatal(err)
	}
	e.h.cfg.Quick = q
	e.onboard(t, anna)
	return e, budgets
}

func TestQuickRecord(t *testing.T) {
	e, _ := setupQuick(t)
	e.meter.allow = false // limits do not touch it
	e.meter.exhausted = true
	e.message(anna, "кофе 350")
	got := e.s.last(t, anna)
	if !strings.Contains(got.text, "Записано: кофе — 350\u00a0₽, еда, сегодня.") {
		t.Fatalf("reply = %q", got.text)
	}
	if len(got.buttons) != 1 || !strings.HasPrefix(got.buttons[0], cbFix+":") {
		t.Fatalf("buttons = %+v", got.buttons)
	}
	if len(e.agent.callers) != 0 {
		t.Error("the agent was called")
	}
	if len(e.agent.remembered) != 1 || !strings.HasPrefix(e.agent.remembered[0], "кофе 350 → Записано") {
		t.Errorf("remembered = %q", e.agent.remembered)
	}
	if ev := e.tracked(events.ExpenseAdded); len(ev) != 1 || ev[0].UserID != anna || ev[0].Props["via"] != events.ViaFast {
		t.Errorf("events = %+v", ev)
	}

	e.message(anna, "зарплата 80000")
	if ev := e.tracked(events.IncomeAdded); len(ev) != 1 {
		t.Errorf("income events = %+v", ev)
	}

	// Correcting: a hint, and deleting works once and only for the owner.
	id := strings.TrimPrefix(got.buttons[0], cbFix+":")
	e.press(anna, cbFix+":"+id)
	hint := e.s.last(t, anna)
	if hint.text != e.t.QuickFixHint || !slices.Equal(hint.buttons, []string{cbFixDelete + ":" + id}) {
		t.Fatalf("hint = %+v", hint)
	}
	e.onboard(t, boris)
	e.press(boris, cbFixDelete+":"+id)
	if got := e.s.last(t, boris); got.text != e.t.QuickGone {
		t.Errorf("someone else's record = %q", got.text)
	}
	e.press(anna, cbFixDelete+":"+id)
	if got := e.s.last(t, anna); got.text != e.t.QuickDeleted {
		t.Errorf("deleted = %q", got.text)
	}
	e.press(anna, cbFixDelete+":"+id)
	if got := e.s.last(t, anna); got.text != e.t.QuickGone {
		t.Errorf("deleted twice = %q", got.text)
	}
}

// setupFlows is setup with the dialogs on.
func setupFlows(t *testing.T) (*env, *budget.Service, *memory.Flows) {
	t.Helper()
	e := setup(t)
	repo := memory.NewOperationRepository()
	svc := ledger.NewService(repo, time.UTC)
	budgets := budget.NewService(memory.NewBudget(repo), svc)
	q, err := quick.New(svc, budgets)
	if err != nil {
		t.Fatal(err)
	}
	flows := memory.NewFlows()
	e.h.cfg.Quick, e.h.cfg.Flows, e.h.cfg.Budget = q, flows, budgets
	e.h.cfg.MiniAppURL = "https://app.example.com"
	return e, budgets, flows
}

// flowButton is the data of a button of a dialog.
func flowButton(step, value string) string { return cbFlow + ":" + step + ":" + value }

func TestOnboarding(t *testing.T) {
	e, budgets, _ := setupFlows(t)
	ctx := context.Background()
	e.message(anna, "/start")
	e.press(anna, cbConsent)
	if got := e.s.last(t, anna); got.text != e.t.OnboardChoice {
		t.Fatalf("after consent = %+v", got)
	}
	e.press(anna, flowButton(stepChoice, "goal"))
	e.message(anna, strings.Repeat("я", 61))
	if got := e.s.last(t, anna); got.text != e.t.RetryTitle || !slices.Contains(got.buttons, cbFlow+":"+stepExit) {
		t.Fatalf("a long title = %+v", got)
	}
	e.message(anna, "Отпуск")
	e.message(anna, "кофе 350") // not an answer: asked again, nothing recorded
	if got := e.s.last(t, anna); got.text != e.t.RetryAmount {
		t.Fatalf("a wrong amount = %+v", got)
	}
	e.message(anna, "/start") // an interruption goes on from the same step
	if got := e.s.last(t, anna); got.text != e.t.OnboardGoalAmount {
		t.Fatalf("resumed = %+v", got)
	}
	e.message(anna, "1,5 млн")
	if got := e.s.last(t, anna); got.text != e.t.OnboardPayday {
		t.Fatalf("after the goal = %+v", got)
	}
	goals, _ := budgets.Goals(ctx, anna)
	if len(goals) != 1 || goals[0].Title != "Отпуск" || !goals[0].Target.Equal(decimal.NewFromInt(1500000)) {
		t.Fatalf("goals = %+v", goals)
	}
	e.message(anna, "42")
	if got := e.s.last(t, anna); got.text != e.t.RetryPayday {
		t.Fatalf("a wrong day = %+v", got)
	}
	e.message(anna, "5 и 20")
	e.message(anna, "40к")
	if got := e.s.last(t, anna); got.text != e.t.OnboardMorning {
		t.Fatalf("after the income = %+v", got)
	}
	plan, _ := budgets.Plan(ctx, anna)
	if plan == nil || !slices.Equal(plan.Paydays, []int{5, 20}) || !plan.ExpectedIncome.Equal(decimal.NewFromInt(40000)) {
		t.Fatalf("plan = %+v", plan)
	}
	e.message(anna, "в 9")
	if got := e.s.last(t, anna); got.text != e.t.RetryButton {
		t.Fatalf("text at a question with buttons = %+v", got)
	}
	e.press(anna, flowButton(stepMorning, "8"))
	if u, _ := e.users.Get(ctx, anna); u.ReminderHour != 8 || !u.Reminders {
		t.Errorf("morning = %+v", u.Notifications)
	}
	if got := e.s.last(t, anna); got.text != e.t.OnboardRecord {
		t.Fatalf("after the morning = %+v", got)
	}
	e.message(anna, "кофе 350")
	sent := e.s.out
	if len(sent) < 2 || !strings.Contains(sent[len(sent)-2].text, "Записано: кофе") {
		t.Fatalf("the first record = %+v", sent[len(sent)-2:])
	}
	got := sent[len(sent)-1]
	if !strings.Contains(got.text, "На сегодня можно потратить") || !slices.Equal(got.buttons, []string{"webapp:https://app.example.com"}) {
		t.Errorf("finale = %+v", got)
	}
	if len(e.tracked(events.OnboardingDone)) != 1 {
		t.Error("onboarding_done was not tracked")
	}
	e.message(anna, "сколько потратил")
	if len(e.agent.callers) != 1 {
		t.Errorf("after onboarding messages reach the agent: %+v", e.agent.callers)
	}
}

func TestOnboardingEveryStepIsSkipped(t *testing.T) {
	e, budgets, _ := setupFlows(t)
	ctx := context.Background()
	e.message(anna, "/start go_goal__tg")
	e.press(anna, cbConsent)
	if got := e.s.last(t, anna); got.text != e.t.OnboardGoalTitle {
		t.Fatalf("a link to a goal skips the choice: %+v", got)
	}
	e.press(anna, flowButton(stepGoalTitle, valueSkip))
	e.press(anna, flowButton(stepPayday, valueSkip))
	e.press(anna, flowButton(stepMorning, valueSkip))
	if u, _ := e.users.Get(ctx, anna); u.Reminders {
		t.Error("the morning message was refused")
	}
	e.press(anna, flowButton(stepRecord, valueSkip))
	if got := e.s.last(t, anna); got.text != e.t.FlowDone {
		t.Errorf("finale = %+v", got)
	}
	if plan, _ := budgets.Plan(ctx, anna); plan != nil {
		t.Errorf("plan = %+v", plan)
	}
}

func TestOnboardingEndsWithTheLimit(t *testing.T) {
	e, budgets, _ := setupFlows(t)
	e.message(anna, "/start go_limit__yt")
	e.press(anna, cbConsent)
	if got := e.s.last(t, anna); got.text != e.t.OnboardPayday {
		t.Fatalf("a link to the limit = %+v", got)
	}
	e.press(anna, flowButton(stepPayday, "5"))
	e.message(anna, "60000")
	e.press(anna, flowButton(stepMorning, "9"))
	e.press(anna, flowButton(stepRecord, valueSkip))
	b, err := budgets.Budget(context.Background(), anna, today(time.Now(), time.UTC))
	if err != nil || b == nil || !b.Ready {
		t.Fatalf("budget = %+v, %v", b, err)
	}
	if got := e.s.last(t, anna); got.text != texts.Fill(e.t.FlowDoneLimit, "limit", report.Money(b.Limit)) {
		t.Errorf("finale = %q", got.text)
	}
}

func TestWritingInsteadOfChoosingJustWorks(t *testing.T) {
	e, _, flows := setupFlows(t)
	e.message(anna, "/start")
	e.press(anna, cbConsent)
	e.message(anna, "кофе 350")
	if got := e.s.last(t, anna); !strings.Contains(got.text, "Записано: кофе") {
		t.Errorf("reply = %+v", got)
	}
	if _, err := flows.Get(context.Background(), anna); !errors.Is(err, flow.ErrNone) {
		t.Errorf("the dialog stayed: %v", err)
	}
}

func TestDialogExpires(t *testing.T) {
	e, _, _ := setupFlows(t)
	e.onboard(t, anna)
	e.press(anna, flowButton(stepChoice, "goal"))
	later := time.Now().Add(flow.TTL + time.Hour)
	e.h.now = func() time.Time { return later }
	e.message(anna, "кофе 350")
	if got := e.s.last(t, anna); !strings.Contains(got.text, "Записано: кофе") {
		t.Errorf("a day later = %+v, want an ordinary record", got)
	}
	e.press(anna, flowButton(stepGoalTitle, valueSkip))
	if got := e.s.last(t, anna); got.text != e.t.FlowStale {
		t.Errorf("an old button = %+v", got)
	}
}

func TestDialogExit(t *testing.T) {
	e, _, _ := setupFlows(t)
	e.onboard(t, anna)
	e.message(anna, "/dream")
	e.message(anna, strings.Repeat("я", 61))
	e.press(anna, cbFlow+":"+stepExit)
	if got := e.s.last(t, anna); got.text != e.t.FlowLeft {
		t.Fatalf("exit = %+v", got)
	}
	e.message(anna, "сколько потратил")
	if len(e.agent.callers) != 1 {
		t.Errorf("after the exit messages reach the agent: %+v", e.agent.callers)
	}
}

func TestDream(t *testing.T) {
	e, budgets, _ := setupFlows(t)
	ctx := context.Background()
	e.onboard(t, anna)
	e.press(anna, flowButton(stepChoice, "just")) // no plan yet
	e.press(anna, flowButton(stepRecord, valueSkip))
	if _, err := budgets.SetPlan(ctx, budget.Plan{UserID: anna, Paydays: []int{5, 20},
		ExpectedIncome: new(decimal.NewFromInt(30000))}); err != nil {
		t.Fatal(err)
	}
	day := today(time.Now(), time.UTC)

	e.message(anna, "/dream")
	if got := e.s.last(t, anna); got.text != e.t.DreamTitle {
		t.Fatalf("/dream = %+v", got)
	}
	e.message(anna, "Ноутбук")
	e.message(anna, "300к")
	e.press(anna, flowButton(stepDreamTerm, "12"))
	e.press(anna, flowButton(stepDreamSaved, valueZero))
	got := e.s.last(t, anna)
	// Two paydays of 30 000: the income of a month is 60 000.
	for _, want := range []string{"25 000 ₽ в месяц", "около 833 ₽ в день", "42% дохода", e.t.DreamStretch} {
		if !strings.Contains(strings.ReplaceAll(got.text, "\u00a0", " "), want) {
			t.Errorf("result lacks %q: %q", want, got.text)
		}
	}
	if len(e.tracked(events.DreamCalculated)) != 1 {
		t.Error("dream_calculated was not tracked")
	}

	e.press(anna, flowButton(stepDreamResult, valueBoost))
	got = e.s.last(t, anna)
	if !strings.Contains(got.text, "38% дохода") || !strings.Contains(got.text, e.t.DreamBoostNote) {
		t.Errorf("with a bigger income = %q", got.text)
	}
	if slices.Contains(got.buttons, flowButton(stepDreamResult, valueBoost)) {
		t.Error("the boost is offered twice")
	}

	e.press(anna, flowButton(stepDreamResult, valueGoal))
	goals, _ := budgets.Goals(ctx, anna)
	if len(goals) != 1 || goals[0].Title != "Ноутбук" || goals[0].Deadline == nil || !goals[0].Deadline.Equal(day.AddDate(0, 12, 0)) {
		t.Fatalf("goals = %+v", goals)
	}
}

func TestDreamFromALinkWithSavedMoney(t *testing.T) {
	e, budgets, _ := setupFlows(t)
	ctx := context.Background()
	e.message(anna, "/start go_dream__reels")
	e.press(anna, cbConsent)
	if u, _ := e.users.Get(ctx, anna); u.FirstSource != "reels" {
		t.Errorf("source = %+v", u)
	}
	if got := e.s.last(t, anna); got.text != e.t.DreamTitle {
		t.Fatalf("a link to the calculator = %+v", got)
	}
	e.press(anna, flowButton(stepDreamTitle, "2"))
	e.message(anna, "120000")
	e.press(anna, flowButton(stepDreamTerm, valueCustom))
	e.message(anna, "вчера")
	if got := e.s.last(t, anna); got.text != e.t.RetryDate {
		t.Fatalf("a wrong date = %+v", got)
	}
	e.message(anna, "через 8 месяцев")
	e.message(anna, "40000")
	got := e.s.last(t, anna)
	plain := strings.ReplaceAll(got.text, "\u00a0", " ")
	if !strings.Contains(plain, "Ноутбук за 120 000 ₽") || !strings.Contains(plain, "10 000 ₽ в месяц") || strings.Contains(plain, "дохода") {
		t.Fatalf("result without an income = %q", got.text)
	}
	if slices.Contains(got.buttons, flowButton(stepDreamResult, valueBoost)) {
		t.Error("no income, nothing to boost")
	}
	e.press(anna, flowButton(stepDreamResult, valueGoal))
	goals, _ := budgets.Goals(ctx, anna)
	if len(goals) != 1 || !goals[0].Saved.Equal(decimal.NewFromInt(40000)) {
		t.Fatalf("the money saved must become the first deposit: %+v", goals)
	}
	// Someone new goes on with the rest of the onboarding.
	if got := e.s.last(t, anna); got.text != e.t.OnboardPayday {
		t.Errorf("after the goal = %+v", got)
	}
}

func TestDreamAlreadySaved(t *testing.T) {
	e, _, _ := setupFlows(t)
	e.onboard(t, anna)
	e.message(anna, "/dream")
	e.message(anna, "Ноутбук")
	e.message(anna, "1000")
	e.press(anna, flowButton(stepDreamTerm, "6"))
	e.message(anna, "1000")
	got := e.s.last(t, anna)
	if !strings.Contains(got.text, "вся сумма уже отложена") {
		t.Errorf("result = %q", got.text)
	}
}

func TestNotificationSettings(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.onboard(t, anna)
	e.message(anna, "/settings")
	got := e.s.last(t, anna)
	if !strings.Contains(got.text, "Утреннее сообщение: в 9:00") {
		t.Fatalf("settings = %q", got.text)
	}
	e.press(anna, cbHour)
	if got := e.s.last(t, anna); !slices.Contains(got.buttons, cbHour+":8") {
		t.Fatalf("hours = %+v", got)
	}
	e.press(anna, cbHour+":8")
	if got := e.s.last(t, anna); !strings.Contains(got.text, "в 8:00") {
		t.Errorf("after picking = %q", got.text)
	}
	e.press(anna, cbHour+":99")
	if u, _ := e.users.Get(ctx, anna); u.ReminderHour != 8 {
		t.Errorf("a forged hour = %d", u.ReminderHour)
	}
	e.press(anna, cbNotify+":"+notifyRates+":off")
	e.press(anna, cbNotify+":"+notifyGoals+":off")
	if u, _ := e.users.Get(ctx, anna); u.ShowRates || u.GoalUpdates || !u.Reminders {
		t.Errorf("notifications = %+v", u.Notifications)
	}
	e.press(anna, cbNotify+":"+notifyMorning+":off")
	got = e.s.last(t, anna)
	if !strings.Contains(got.text, "Утреннее сообщение: выключено") || slices.Contains(got.buttons, cbHour) {
		t.Errorf("morning off = %+v", got)
	}
}

func TestParsePaydays(t *testing.T) {
	for text, want := range map[string]string{"5": "5", "10 числа": "10", "5 и 20": "5,20", "5, 20": "5,20", "5-го и 20-го": "5,20", "1 15": "1,15"} {
		if got, ok := parsePaydays(text); !ok || got != want {
			t.Errorf("parsePaydays(%q) = %q, %v, want %q", text, got, ok, want)
		}
	}
	for _, text := range []string{"", "0", "32", "5 и 40", "пятого", "5 6 7"} {
		if got, ok := parsePaydays(text); ok {
			t.Errorf("parsePaydays(%q) = %q", text, got)
		}
	}
}

func TestQuickLeavesTheRestToTheAgent(t *testing.T) {
	e, budgets := setupQuick(t)
	ctx := context.Background()
	e.message(anna, "сколько потратил на кофе")
	if len(e.agent.callers) != 1 {
		t.Fatalf("a question must reach the agent: %+v", e.agent.callers)
	}
	if _, err := budgets.CreatePayment(ctx, budget.RegularPayment{
		UserID: anna, Title: "Аренда", Amount: decimal.NewFromInt(45000), Day: 1, Category: ledger.CategoryHousing,
	}); err != nil {
		t.Fatal(err)
	}
	e.message(anna, "аренда 45000")
	if len(e.agent.callers) != 2 {
		t.Errorf("a regular payment must reach the agent: %+v", e.agent.callers)
	}
}

func TestPackageExhausted(t *testing.T) {
	e := setup(t)
	e.onboard(t, anna)
	e.meter.exhausted = true
	e.message(anna, "сколько потратил за неделю")
	if len(e.agent.callers) != 0 {
		t.Error("the agent was called with the package used up")
	}
	if got := e.s.last(t, anna); got.text != e.t.AgentResting {
		t.Errorf("reply = %q", got.text)
	}
	if ev := e.tracked(events.AILimitHit); len(ev) != 1 || ev[0].Props["reason"] != "package" {
		t.Errorf("events = %+v", ev)
	}
}

func TestBlockedBotIsTracked(t *testing.T) {
	e := setup(t)
	e.s.forbidden = true
	err := e.h.Send(context.Background(), e.s, anna, "итоги недели")
	if !errors.Is(err, ErrUndeliverable) {
		t.Fatalf("err = %v", err)
	}
	if ev := e.tracked(events.BotBlocked); len(ev) != 1 || ev[0].UserID != anna {
		t.Errorf("events = %+v", ev)
	}
}

func TestLeaveAndKick(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.onboard(t, anna)
	e.onboard(t, boris)
	head, _ := e.users.Get(ctx, anna)
	join := func() {
		inv, err := e.family.CreateInvite(ctx, head)
		if err != nil {
			t.Fatal(err)
		}
		_, code, _ := strings.Cut(inv.Link, "start=")
		e.press(boris, cbJoin+":"+code)
	}

	join()
	e.message(boris, "/family")
	if got := e.s.last(t, boris); !strings.Contains(got.text, "Вы в семье Анна") || !slices.Equal(got.buttons, []string{cbLeave}) {
		t.Fatalf("member's /family = %+v", got)
	}
	e.press(boris, cbKickYes+":1") // a member cannot remove anyone, whatever the client sends
	if a, _ := e.users.Get(ctx, anna); a.FamilyID != head.FamilyID {
		t.Fatal("a member removed the head")
	}
	e.press(boris, cbLeaveYes)
	if got := e.s.last(t, boris); got.text != e.t.Left {
		t.Errorf("left = %q", got.text)
	}
	if got := e.s.last(t, anna); got.text != texts.Fill(e.t.MemberLeft, "name", "Борис") {
		t.Errorf("head notified = %q", got.text)
	}

	join()
	e.message(anna, "/family")
	if got := e.s.last(t, anna); !slices.Contains(got.buttons, cbKick+":2") {
		t.Fatalf("head's /family = %+v", got)
	}
	e.press(anna, cbKickYes+":2")
	if got := e.s.last(t, boris); got.text != e.t.YouWereRemoved {
		t.Errorf("removed member notified = %q", got.text)
	}
	if b, _ := e.users.Get(ctx, boris); b.FamilyID == head.FamilyID {
		t.Error("Boris is still in the family")
	}
}

func TestSettings(t *testing.T) {
	e := setup(t)
	e.onboard(t, anna)
	e.message(anna, "/settings")
	if got := e.s.last(t, anna); !strings.Contains(got.text, e.t.WeeklyOn) || !strings.Contains(got.text, "UTC+0") {
		t.Errorf("settings = %q", got.text)
	}
	e.press(anna, cbWeekly+":off")
	if got := e.s.last(t, anna); !strings.Contains(got.text, e.t.WeeklyOff) {
		t.Errorf("after turning reports off = %q", got.text)
	}
	e.press(anna, cbTimezone)
	if got := e.s.last(t, anna); !slices.Contains(got.buttons, cbTimezone+":Asia/Omsk") {
		t.Errorf("timezone picker = %+v", got)
	}
	e.press(anna, cbTimezone+":Asia/Omsk")
	if got := e.s.last(t, anna); !strings.Contains(got.text, "Омск, UTC+6") {
		t.Errorf("after choosing Omsk = %q", got.text)
	}
	e.message(anna, "кофе 350")
	if loc := e.agent.callers[0].Location; loc.String() != "Asia/Omsk" {
		t.Errorf("agent got timezone %s, want the user's", loc)
	}
}

func TestDeleteAccount(t *testing.T) {
	e := setup(t)
	e.onboard(t, anna)
	e.message(anna, "/delete_me")
	if got := e.s.last(t, anna); got.text != e.t.DeleteConfirm || got.buttons[0] != cbDelete {
		t.Fatalf("confirmation = %+v", got)
	}
	e.press(anna, cbDelete)
	if got := e.s.last(t, anna); got.text != e.t.Deleted {
		t.Errorf("deleted = %q", got.text)
	}
	if _, err := e.users.Get(context.Background(), anna); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("user still stored: %v", err)
	}
}

func TestFeedbackReachesAdmin(t *testing.T) {
	e := setup(t)
	e.onboard(t, anna)
	e.message(anna, "/feedback")
	if got := e.s.last(t, anna); got.text != e.t.FeedbackUsage {
		t.Errorf("empty feedback = %q", got.text)
	}
	e.message(anna, "/feedback хочу цели")
	if got := e.s.last(t, admin); !strings.Contains(got.text, "Анна (@анна, id 1)") || !strings.Contains(got.text, "хочу цели") {
		t.Errorf("admin got %q", got.text)
	}
	if got := e.s.last(t, anna); got.text != e.t.FeedbackThanks {
		t.Errorf("thanks = %q", got.text)
	}
}

func TestDailyLimit(t *testing.T) {
	e := setup(t)
	e.onboard(t, anna)
	e.meter.allow = false
	e.message(anna, "кофе 350")
	if len(e.agent.callers) != 0 || e.s.last(t, anna).text != e.t.LimitReached {
		t.Errorf("callers = %d, last = %q", len(e.agent.callers), e.s.last(t, anna).text)
	}
}

func TestBlockedUser(t *testing.T) {
	e := setup(t)
	e.onboard(t, anna)
	e.users.Block(anna)
	e.message(anna, "кофе 350")
	if len(e.agent.callers) != 0 || e.s.last(t, anna).text != e.t.Blocked {
		t.Errorf("blocked user served: %q", e.s.last(t, anna).text)
	}
}

type failingFamily struct{ Family }

func (failingFamily) Admit(context.Context, user.User) (user.User, error) {
	return user.User{}, errors.New("db down")
}

func TestAdmitFailure(t *testing.T) {
	e := setup(t)
	e.h.family = failingFamily{e.family}
	e.message(anna, "кофе 350")
	if len(e.agent.callers) != 0 || e.s.last(t, anna).text != e.t.Failure {
		t.Errorf("callers = %d, sent = %+v", len(e.agent.callers), e.s.out)
	}
}

func TestCommandsDoNotReachTheAgent(t *testing.T) {
	e := setup(t)
	e.onboard(t, anna)
	e.message(anna, "/start")
	e.message(anna, "/help")
	e.message(anna, "/unknown")
	if len(e.agent.callers) != 0 || len(e.agent.resets) != 2 { // /start and /help start the dialog over
		t.Errorf("callers = %d resets = %v", len(e.agent.callers), e.agent.resets)
	}
	if got := e.s.last(t, anna); got.text != e.t.Help {
		t.Errorf("unknown command = %q, want help", got.text)
	}
}

func TestIgnoredAndRejectedMessages(t *testing.T) {
	tests := []struct {
		name   string
		update *models.Update
		want   func(texts.Bot) string // nil means no reply
	}{
		{"edited message", &models.Update{EditedMessage: &models.Message{}}, nil},
		{"group chat", &models.Update{Message: &models.Message{
			From: &models.User{ID: anna}, Chat: models.Chat{ID: -5, Type: models.ChatTypeGroup}, Text: "кофе 350",
		}}, func(t texts.Bot) string { return t.Private }},
		{"sticker", textMessage(""), func(t texts.Bot) string { return t.NotText }},
		{"too long", textMessage(strings.Repeat("а", maxInputRunes+1)), func(t texts.Bot) string { return t.TooLong }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setup(t)
			e.onboard(t, anna)
			before := len(e.s.out)
			e.h.handle(context.Background(), e.s, tt.update)
			if len(e.agent.callers) != 0 {
				t.Error("message reached the agent")
			}
			sent := e.s.out[before:]
			if tt.want == nil && len(sent) != 0 || tt.want != nil && (len(sent) != 1 || sent[0].text != tt.want(e.t)) {
				t.Errorf("sent = %+v", sent)
			}
		})
	}
}

func textMessage(text string) *models.Update {
	return &models.Update{Message: &models.Message{
		From: &models.User{ID: anna, FirstName: "Анна"},
		Chat: models.Chat{ID: anna, Type: models.ChatTypePrivate},
		Text: text,
	}}
}

func TestAgentFailure(t *testing.T) {
	e := setup(t)
	e.onboard(t, anna)
	e.agent.err = errors.New("gigachat: HTTP 500")
	e.message(anna, "кофе 350")
	if got := e.s.last(t, anna); got.text != e.t.Failure {
		t.Errorf("sent = %q", got.text)
	}
}

func TestIsCommand(t *testing.T) {
	for text, want := range map[string]bool{
		"/start": true, "/start@SavepointBot": true, "/START": true, "/start hello": true,
		"/stats": false, "start": false,
	} {
		if got := isCommand(text, "/start"); got != want {
			t.Errorf("isCommand(%q) = %v", text, got)
		}
	}
}

func TestReplyIsResentAfterNetworkFailure(t *testing.T) {
	e := setup(t)
	e.onboard(t, anna)
	e.s.failures = e.s.attempts + 2
	before := e.s.attempts
	e.message(anna, "кофе 350")
	if e.s.attempts-before != 3 || e.s.last(t, anna).text != "Записано" {
		t.Errorf("attempts = %d, last = %q", e.s.attempts-before, e.s.last(t, anna).text)
	}
}

func TestReplyIsSentAfterRequestContextExpired(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // e.g. the LLM call ran out of time
	e.h.send(ctx, e.s, anna, "x")
	if len(e.s.out) != 1 {
		t.Errorf("sent = %+v, want the message delivered", e.s.out)
	}
}

func TestPermanentErrorsAreNotRetried(t *testing.T) {
	e := setup(t)
	s := &blockedSender{fakeSender: e.s}
	err := e.h.Send(context.Background(), s, anna, "x")
	if !errors.Is(err, ErrUndeliverable) || s.attempts != 1 {
		t.Errorf("err = %v after %d attempts, want ErrUndeliverable after 1", err, s.attempts)
	}
}

// blockedSender behaves like Telegram for a user who blocked the bot.
type blockedSender struct {
	*fakeSender
	attempts int
}

func (b *blockedSender) SendMessage(context.Context, *bot.SendMessageParams) (*models.Message, error) {
	b.attempts++
	return nil, fmt.Errorf("%w, Forbidden: bot was blocked by the user", bot.ErrorForbidden)
}

func TestSendGivesUp(t *testing.T) {
	e := setup(t)
	e.s.failures = 100
	e.h.send(context.Background(), e.s, anna, "x")
	if e.s.attempts != sendAttempts || len(e.s.out) != 0 {
		t.Errorf("attempts = %d", e.s.attempts)
	}
}

func TestTimezoneLabels(t *testing.T) {
	moscow, _ := time.LoadLocation("Europe/Moscow")
	if got := timezoneName(moscow); got != "Москва, UTC+3" {
		t.Errorf("Moscow = %q", got)
	}
	kolkata, _ := time.LoadLocation("Asia/Kolkata")
	if got := timezoneName(kolkata); got != "Asia/Kolkata, UTC+5:30" {
		t.Errorf("Kolkata = %q", got)
	}
}

func TestAppCommand(t *testing.T) {
	e := setup(t)
	e.onboard(t, anna)
	e.message(anna, "/app")
	if got := e.s.last(t, anna); got.text != e.t.AppMissing || len(got.buttons) != 0 {
		t.Errorf("without a mini app: %+v", got)
	}

	e.h.cfg.MiniAppURL = "https://app.example.com"
	e.message(anna, "/app")
	if got := e.s.last(t, anna); got.text != e.t.AppOpen || !slices.Equal(got.buttons, []string{"webapp:https://app.example.com"}) {
		t.Errorf("with a mini app: %+v", got)
	}
	if MenuButton(e.t, "") != nil || MenuButton(e.t, "https://app.example.com") == nil {
		t.Error("the menu button must exist only with a mini app")
	}
	if len(Commands(e.t, true)) != len(Commands(e.t, false))+1 {
		t.Error("/app must be in the command menu only with a mini app")
	}
}

func TestSendWithApp(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if err := e.h.SendWithApp(ctx, e.s, anna, "Сегодня можно потратить 1 300 ₽.", "morning"); err != nil {
		t.Fatal(err)
	}
	if got := e.s.last(t, anna); len(got.buttons) != 0 {
		t.Errorf("without a mini app the reminder has no button: %+v", got)
	}
	e.h.cfg.MiniAppURL = "https://app.example.com"
	e.s.failures = e.s.attempts + 1 // the next attempt fails: retried like replies
	if err := e.h.SendWithApp(ctx, e.s, anna, "Сегодня можно потратить 1 300 ₽.", "morning"); err != nil {
		t.Fatal(err)
	}
	if got := e.s.last(t, anna); !slices.Equal(got.buttons, []string{"webapp:https://app.example.com?from=morning"}) {
		t.Errorf("reminder buttons = %+v", got)
	}
}

type fakeStats struct{ asked int }

func (f *fakeStats) Snapshot(_ context.Context, now, _, _, _ time.Time) (stats.Snapshot, error) {
	f.asked++
	return stats.Snapshot{At: now, Users: 3, Consented: 2}, nil
}

func TestAdminCommands(t *testing.T) {
	e := setup(t)
	st := &fakeStats{}
	e.h.cfg.Stats = st
	e.onboard(t, anna)
	e.onboard(t, admin)

	// For everyone else these are unknown commands.
	for _, cmd := range []string{"/stats", "/ban 99", "/unban 99"} {
		e.message(anna, cmd)
		if got := e.s.last(t, anna); got.text != e.t.Help {
			t.Errorf("%s from a user: %q", cmd, got.text)
		}
	}
	if st.asked != 0 {
		t.Error("a user got the stats")
	}

	e.message(admin, "/stats")
	if got := e.s.last(t, admin); !strings.Contains(got.text, "Пользователи: 3 (приняли политику 2") {
		t.Errorf("stats = %q", got.text)
	}

	for _, tt := range []struct{ cmd, want string }{
		{"/ban", e.t.AdminBanUsage},
		{"/ban abc", e.t.AdminBanUsage},
		{"/ban 99", e.t.AdminSelf},
		{"/ban 404", texts.Fill(e.t.AdminUnknownUser, "id", "404")},
		{"/ban 1", texts.Fill(e.t.AdminBanned, "id", "1")},
	} {
		e.message(admin, tt.cmd)
		if got := e.s.last(t, admin); got.text != tt.want {
			t.Errorf("%s: %q, want %q", tt.cmd, got.text, tt.want)
		}
	}
	e.message(anna, "кофе 350")
	if got := e.s.last(t, anna); got.text != e.t.Blocked {
		t.Errorf("banned user served: %q", got.text)
	}
	e.message(admin, "/unban 1")
	e.message(anna, "кофе 350")
	if got := e.s.last(t, anna); got.text != "Записано" {
		t.Errorf("unbanned user: %q", got.text)
	}
	if len(AdminCommands(e.t, true)) != len(Commands(e.t, true))+3 {
		t.Error("the owner's menu must add /stats, /ban and /unban")
	}
}
