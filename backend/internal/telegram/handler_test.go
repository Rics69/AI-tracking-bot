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

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
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
}

func (f *fakeSender) SendMessage(_ context.Context, p *bot.SendMessageParams) (*models.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
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
}

func (f *fakeAgent) Reply(_ context.Context, c agent.Caller, _ string) (string, error) {
	f.callers = append(f.callers, c)
	return f.reply, f.err
}

func (f *fakeAgent) Reset(_ context.Context, id int64) { f.resets = append(f.resets, id) }

type fakeMeter struct{ allow bool }

func (f *fakeMeter) Allow(context.Context, int64, time.Time) (bool, error) { return f.allow, nil }

type env struct {
	h      *Handler
	agent  *fakeAgent
	meter  *fakeMeter
	users  *memory.Users
	family *family.Service
	s      *fakeSender
	t      texts.Bot
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
	e := &env{agent: &fakeAgent{reply: "Записано"}, meter: &fakeMeter{allow: true}, users: users, family: fam, s: &fakeSender{}, t: tx}
	e.h = NewHandler(e.agent, fam, e.meter, Config{
		ReplyTimeout: time.Minute, Location: time.UTC, AdminID: admin, Texts: tx,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	if got := e.s.last(t, boris); !slices.Equal(got.buttons, []string{cbConsent + ":" + code}) {
		t.Fatalf("new user with an invite = %+v, want consent carrying the code", got)
	}
	e.press(boris, cbConsent+":"+code)
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
