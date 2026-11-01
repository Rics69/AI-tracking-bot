package telegram

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot/models"
	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/flow"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/quick"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/report"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/startparam"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// Budget is the part of budget.Service the dialogs write through, the same
// tables the mini app and the agent use.
type Budget interface {
	Plan(ctx context.Context, userID int64) (*budget.Plan, error)
	SetPlan(ctx context.Context, p budget.Plan) (budget.Plan, error)
	Budget(ctx context.Context, userID int64, today time.Time) (*budget.Budget, error)
	CreateGoal(ctx context.Context, g budget.Goal, today time.Time) (budget.Goal, error)
	Deposit(ctx context.Context, userID, goalID int64, amount decimal.Decimal, date *time.Time, today time.Time) (budget.Goal, error)
	Goal(ctx context.Context, userID, id int64) (budget.Goal, error)
	Goals(ctx context.Context, userID int64) ([]budget.Goal, error)
	Payments(ctx context.Context, userID int64) ([]budget.RegularPayment, error)
}

const (
	flowOnboarding = "onboarding"
	flowDream      = "dream"

	maxGoalTitle = 60 // runes

	// Keys of flow.State.Data.
	keyNew      = "new" // right after consent: the dialog goes on to the next topic
	keyTitle    = "title"
	keyPaydays  = "paydays"
	keyPrice    = "price"
	keySaved    = "saved"
	keyDeadline = "deadline"
	keyBoost    = "boost"
)

// Where a step leads besides the name of the next one.
const (
	stay  = "-" // the step has answered by itself
	again = "?" // the input did not fit: ask once more
	done  = "." // the dialog is over
)

// session is one update handled inside a dialog.
type session struct {
	h         *Handler
	ctx       context.Context
	s         Sender
	chatID    int64
	messageID int // the message with the pressed button; 0 after a text
	u         user.User
	day       time.Time // today for the user
	st        flow.State
}

// step is one question of a dialog. text is nil where the answer is a button:
// with retry set the question is asked again, without it the message is not
// an answer at all and the dialog steps aside.
type step struct {
	ask   func(*session) view
	text  func(*session, string) string
	press func(*session, string) string
	retry func(texts.Bot) string
}

var steps map[string]step

func init() {
	steps = map[string]step{
		stepChoice:     {ask: askChoice, press: pressChoice},
		stepGoalTitle:  {ask: askGoalTitle, text: textGoalTitle, press: pressSkipGoal, retry: retryTitle},
		stepGoalAmount: {ask: askGoalAmount, text: textGoalAmount, press: pressSkipGoal, retry: retryAmount},
		stepPayday:     {ask: askPayday, text: textPayday, press: pressPayday, retry: retryPayday},
		stepIncome:     {ask: askIncome, text: textIncome, press: pressIncome, retry: retryAmount},
		stepMorning:    {ask: askMorning, press: pressMorning, retry: retryButton},
		stepRecord:     {ask: askRecord, press: func(*session, string) string { return done }},

		stepDreamTitle:  {ask: askDreamTitle, text: textDreamTitle, press: pressDreamTitle, retry: retryTitle},
		stepDreamPrice:  {ask: askDreamPrice, text: textDreamPrice, press: pressSkipGoal, retry: retryAmount},
		stepDreamTerm:   {ask: askDreamTerm, press: pressDreamTerm, retry: retryButton},
		stepDreamDate:   {ask: askDreamDate, text: textDreamDate, press: pressSkipGoal, retry: retryDate},
		stepDreamSaved:  {ask: askDreamSaved, text: textDreamSaved, press: pressDreamSaved, retry: retryAmount},
		stepDreamResult: {ask: askDreamResult, press: pressDreamResult, retry: retryButton},

		stepLevel: {ask: askLevel, press: pressLevel, retry: retryButton},

		stepReviewStart:    {ask: askReviewStart, press: pressReviewStart, retry: retryButton},
		stepReviewAge:      {ask: askReviewAge, press: pressReviewAge, retry: retryButton},
		stepReviewDebts:    {ask: askReviewDebts, text: textReviewDebts, press: pressReviewDebts, retry: retryAmount},
		stepReviewRate:     {ask: askReviewRate, text: textReviewRate, retry: retryRate},
		stepReviewGoal:     {ask: askReviewGoal, text: textReviewGoal, retry: retryLong},
		stepReviewQuestion: {ask: askReviewQuestion, text: textReviewQuestion, retry: retryLong},
		stepReviewConsent:  {ask: askReviewConsent, press: pressReviewConsent, retry: retryButton},
	}
	for _, q := range []struct {
		step, key string
		text      func(texts.Bot) string
	}{
		{stepReviewIncome, keyIncome, func(t texts.Bot) string { return t.ReviewIncome }},
		{stepReviewExpenses, keyExpenses, func(t texts.Bot) string { return t.ReviewExpenses }},
		{stepReviewSavings, keySavings, func(t texts.Bot) string { return t.ReviewSavings }},
	} {
		ask, answer := amountStep(q.text, q.step, q.key)
		steps[q.step] = step{ask: ask, text: answer, retry: retryAmount}
	}
}

func retryTitle(t texts.Bot) string  { return t.RetryTitle }
func retryAmount(t texts.Bot) string { return t.RetryAmount }
func retryPayday(t texts.Bot) string { return t.RetryPayday }
func retryDate(t texts.Bot) string   { return t.RetryDate }
func retryButton(t texts.Bot) string { return t.RetryButton }
func retryRate(t texts.Bot) string   { return t.RetryRate }
func retryLong(t texts.Bot) string   { return t.RetryLong }

// Dialog mechanics.

func (h *Handler) session(ctx context.Context, s Sender, chatID int64, messageID int, u user.User, st flow.State) *session {
	return &session{h: h, ctx: ctx, s: s, chatID: chatID, messageID: messageID, u: u,
		day: today(h.now(), u.Location(h.cfg.Location)), st: st}
}

// activeFlow returns the user's dialog; one left for too long is dropped. A
// broken store must not stop the bot: the message then goes the usual way.
func (h *Handler) activeFlow(ctx context.Context, userID int64) (flow.State, bool) {
	if h.cfg.Flows == nil {
		return flow.State{}, false
	}
	st, err := h.cfg.Flows.Get(ctx, userID)
	if errors.Is(err, flow.ErrNone) {
		return flow.State{}, false
	}
	if err != nil {
		h.log.ErrorContext(ctx, "get flow", "user_id", userID, "err", err)
		return flow.State{}, false
	}
	if st.Expired(h.now()) {
		h.dropFlow(ctx, userID)
		return flow.State{}, false
	}
	return st, true
}

func (h *Handler) dropFlow(ctx context.Context, userID int64) {
	if err := h.cfg.Flows.Delete(ctx, userID); err != nil {
		h.log.ErrorContext(ctx, "delete flow", "user_id", userID, "err", err)
	}
}

// show puts a message on the screen: in place of the pressed button's message,
// or as a new one.
func (se *session) show(v view) {
	se.h.reply(se.ctx, se.s, se.chatID, se.messageID, v)
	se.messageID = 0
}

// fail tells the user something broke; the step stays, so they can repeat it.
func (se *session) fail(err error) string {
	se.h.log.ErrorContext(se.ctx, "flow step", "user_id", se.u.TelegramID, "step", se.st.Step, "err", err)
	se.show(view{text: se.h.t.Failure})
	return stay
}

// advance acts on what a step decided.
func (se *session) advance(next string) {
	switch next {
	case stay:
	case again:
		se.show(view{
			text:    steps[se.st.Step].retry(se.h.t),
			buttons: [][]models.InlineKeyboardButton{row(button(se.h.t.ExitButton, cbFlow+":"+stepExit))},
		})
	case done:
		se.finish()
	default:
		se.st.Step = next
		if err := se.h.cfg.Flows.Save(se.ctx, se.st); err != nil {
			se.fail(err)
			return
		}
		if se.st.Flow == flowOnboarding {
			se.h.cfg.Events.Track(se.ctx, se.u.TelegramID, events.OnboardingStep, events.Props{"step": next})
		}
		se.show(steps[next].ask(se))
	}
}

// finish ends the dialog with today's limit, if there is one by now, and the
// way to the mini app.
func (se *session) finish() {
	h := se.h
	h.dropFlow(se.ctx, se.u.TelegramID)
	if se.isNew() {
		h.cfg.Events.Track(se.ctx, se.u.TelegramID, events.OnboardingDone, nil)
		h.rewardReferral(se.ctx, se.s, se.u)
	}
	text := h.t.FlowDone
	b, err := h.cfg.Budget.Budget(se.ctx, se.u.TelegramID, se.day)
	if err != nil {
		h.log.ErrorContext(se.ctx, "flow: compute the limit", "user_id", se.u.TelegramID, "err", err)
	} else if b != nil && b.Ready {
		text = texts.Fill(h.t.FlowDoneLimit, "limit", report.Money(b.Limit))
	}
	v := view{text: text}
	if h.cfg.MiniAppURL != "" {
		v.buttons = [][]models.InlineKeyboardButton{{
			{Text: h.t.AppButton, WebApp: &models.WebAppInfo{URL: h.cfg.MiniAppURL}},
		}}
	}
	se.show(v)
}

func (se *session) isNew() bool { return se.st.Data[keyNew] != "" }

// flowText gives a message to the user's dialog. It reports whether the
// message was its answer; after, if any, runs once the bot has handled it.
func (h *Handler) flowText(ctx context.Context, s Sender, chatID int64, u user.User, text string) (handled bool, after func()) {
	st, ok := h.activeFlow(ctx, u.TelegramID)
	if !ok {
		return false, nil
	}
	se := h.session(ctx, s, chatID, 0, u, st)
	cur := steps[st.Step]
	switch {
	case st.Step == stepRecord:
		// The first record goes the usual way, then the dialog ends.
		h.dropFlow(ctx, u.TelegramID)
		return false, se.finish
	case cur.text == nil && cur.retry == nil:
		h.dropFlow(ctx, u.TelegramID) // no choice made: the user just started writing
		return false, nil
	case cur.text == nil:
		se.advance(again)
	default:
		se.advance(cur.text(se, text))
	}
	return true, nil
}

// flowPress handles a button of a dialog, "<step>:<value>".
func (h *Handler) flowPress(ctx context.Context, s Sender, chatID int64, messageID int, u user.User, arg string) {
	name, value, _ := strings.Cut(arg, ":")
	st, ok := h.activeFlow(ctx, u.TelegramID)
	if name == stepExit {
		if ok {
			h.dropFlow(ctx, u.TelegramID)
		}
		h.reply(ctx, s, chatID, messageID, view{text: h.t.FlowLeft})
		return
	}
	if !ok || st.Step != name || steps[name].press == nil {
		h.reply(ctx, s, chatID, messageID, view{text: h.t.FlowStale})
		return
	}
	se := h.session(ctx, s, chatID, messageID, u, st)
	se.advance(steps[name].press(se, value))
}

// begin starts a dialog at a step, replacing whatever was going on.
func (h *Handler) begin(ctx context.Context, s Sender, chatID int64, u user.User, name, first string, isNew bool) {
	st := flow.State{UserID: u.TelegramID, Flow: name, Data: map[string]string{}}
	if isNew {
		st.Data[keyNew] = "1"
	}
	h.session(ctx, s, chatID, 0, u, st).advance(first)
}

// dialogsOn reports whether the bot was given what the dialogs need.
func (h *Handler) dialogsOn() bool { return h.cfg.Flows != nil && h.cfg.Budget != nil }

// route starts the dialog a link leads to, or onboarding for someone who has
// just accepted the policy. It reports whether a dialog started.
func (h *Handler) route(ctx context.Context, s Sender, chatID int64, u user.User, p startparam.Param, isNew bool) bool {
	if !h.dialogsOn() {
		return false
	}
	switch {
	case p.Feature == startparam.FeatureDream:
		h.begin(ctx, s, chatID, u, flowDream, stepDreamTitle, isNew)
	case p.Feature == startparam.FeatureGoal:
		h.begin(ctx, s, chatID, u, flowOnboarding, stepGoalTitle, isNew)
	case p.Feature == startparam.FeatureLimit:
		h.begin(ctx, s, chatID, u, flowOnboarding, stepPayday, isNew)
	case p.Feature == startparam.FeatureReview && h.gameOn():
		h.begin(ctx, s, chatID, u, flowReview, stepReviewStart, false)
	case p.Kind == startparam.Challenge && h.gameOn() && h.offerChallenge(ctx, s, chatID, p.Slug):
	case isNew:
		h.begin(ctx, s, chatID, u, flowOnboarding, stepChoice, isNew)
	default:
		return false
	}
	return true
}

// resume asks the current question again: an interrupted dialog goes on from
// the same step.
func (h *Handler) resume(ctx context.Context, s Sender, chatID int64, u user.User) bool {
	st, ok := h.activeFlow(ctx, u.TelegramID)
	if !ok {
		return false
	}
	se := h.session(ctx, s, chatID, 0, u, st)
	se.show(steps[st.Step].ask(se))
	return true
}

func (h *Handler) startDream(ctx context.Context, s Sender, chatID int64, u user.User) {
	if !h.dialogsOn() {
		h.send(ctx, s, chatID, h.t.Help)
		return
	}
	h.begin(ctx, s, chatID, u, flowDream, stepDreamTitle, false)
}

func stepButton(label, name, value string) models.InlineKeyboardButton {
	return button(label, cbFlow+":"+name+":"+value)
}

// Onboarding.

const (
	stepExit       = "exit"
	stepChoice     = "choice"
	stepGoalTitle  = "gtitle"
	stepGoalAmount = "gamount"
	stepPayday     = "payday"
	stepIncome     = "income"
	stepMorning    = "morning"
	stepRecord     = "record"

	valueSkip = "skip"
)

func askChoice(se *session) view {
	t := se.h.t
	return view{text: t.OnboardChoice, buttons: [][]models.InlineKeyboardButton{
		row(stepButton(t.OnboardDream, stepChoice, "dream")),
		row(stepButton(t.OnboardGoal, stepChoice, "goal")),
		row(stepButton(t.OnboardJust, stepChoice, "just")),
	}}
}

func pressChoice(se *session, value string) string {
	switch value {
	case "dream":
		se.st.Flow = flowDream
		return stepDreamTitle
	case "goal":
		return stepGoalTitle
	default:
		return stepRecord
	}
}

func skipRow(se *session, name string) []models.InlineKeyboardButton {
	return row(stepButton(se.h.t.SkipButton, name, valueSkip))
}

func askGoalTitle(se *session) view {
	return view{text: se.h.t.OnboardGoalTitle, buttons: [][]models.InlineKeyboardButton{skipRow(se, stepGoalTitle)}}
}

func validTitle(text string) bool {
	return text != "" && utf8.RuneCountInString(text) <= maxGoalTitle
}

func textGoalTitle(se *session, text string) string {
	if !validTitle(text) {
		return again
	}
	se.st.Data[keyTitle] = text
	return stepGoalAmount
}

func askGoalAmount(se *session) view {
	return view{text: se.h.t.OnboardGoalAmount, buttons: [][]models.InlineKeyboardButton{skipRow(se, stepGoalAmount)}}
}

func textGoalAmount(se *session, text string) string {
	amount, ok := quick.Amount(text)
	if !ok {
		return again
	}
	title := se.st.Data[keyTitle]
	if _, err := se.createGoal(title, amount, nil); err != nil {
		return se.fail(err)
	}
	se.show(view{text: texts.Fill(se.h.t.OnboardGoalSaved, "title", title, "amount", report.Money(amount))})
	return se.afterGoal()
}

func pressSkipGoal(se *session, _ string) string { return se.afterGoal() }

func (se *session) createGoal(title string, target decimal.Decimal, deadline *time.Time) (budget.Goal, error) {
	g, err := se.h.cfg.Budget.CreateGoal(se.ctx, budget.Goal{
		UserID: se.u.TelegramID, Title: title, Target: target, Deadline: deadline,
	}, se.day)
	if err == nil {
		se.h.cfg.Events.Track(se.ctx, se.u.TelegramID, events.GoalCreated, events.Props{"flow": se.st.Flow})
	}
	return g, err
}

// afterGoal goes on to the money for someone new; a link to a goal for someone
// who is already here ends with the goal.
func (se *session) afterGoal() string {
	if !se.isNew() {
		return done
	}
	se.st.Flow = flowOnboarding
	return stepPayday
}

// paydayChoices are the buttons of the payday step; other days are written.
var paydayChoices = []int{1, 5, 10, 15, 20, 25}

func askPayday(se *session) view {
	days := make([]models.InlineKeyboardButton, len(paydayChoices))
	for i, d := range paydayChoices {
		n := strconv.Itoa(d)
		days[i] = stepButton(n, stepPayday, n)
	}
	return view{text: se.h.t.OnboardPayday, buttons: [][]models.InlineKeyboardButton{days[:3], days[3:], skipRow(se, stepPayday)}}
}

var paydaysText = regexp.MustCompile(`^(\d{1,2})(?:-?го)?(?:(?: и |, ?| )(\d{1,2})(?:-?го)?)?(?: числа)?$`)

// parsePaydays reads "5", "10 числа", "5 и 20". The caller's plan checks the range.
func parsePaydays(text string) (string, bool) {
	m := paydaysText.FindStringSubmatch(strings.Join(strings.Fields(strings.ToLower(text)), " "))
	if m == nil {
		return "", false
	}
	days := m[1]
	if m[2] != "" {
		days += "," + m[2]
	}
	for _, d := range strings.Split(days, ",") {
		if n, _ := strconv.Atoi(d); n < 1 || n > 31 {
			return "", false
		}
	}
	return days, true
}

func textPayday(se *session, text string) string {
	days, ok := parsePaydays(text)
	if !ok {
		return again
	}
	se.st.Data[keyPaydays] = days
	return stepIncome
}

func pressPayday(se *session, value string) string {
	if value == valueSkip {
		return se.afterMoney()
	}
	se.st.Data[keyPaydays] = value
	return stepIncome
}

func askIncome(se *session) view {
	return view{text: se.h.t.OnboardIncome, buttons: [][]models.InlineKeyboardButton{skipRow(se, stepIncome)}}
}

func textIncome(se *session, text string) string {
	amount, ok := quick.Amount(text)
	if !ok {
		return again
	}
	return se.savePlan(&amount)
}

func pressIncome(se *session, _ string) string { return se.savePlan(nil) }

// savePlan keeps what the plan already had: this dialog asks only about the pay.
func (se *session) savePlan(income *decimal.Decimal) string {
	plan := budget.Plan{UserID: se.u.TelegramID}
	old, err := se.h.cfg.Budget.Plan(se.ctx, se.u.TelegramID)
	if err != nil {
		return se.fail(err)
	}
	if old != nil {
		plan = *old
	}
	plan.Paydays = nil
	for _, d := range strings.Split(se.st.Data[keyPaydays], ",") {
		n, err := strconv.Atoi(d)
		if err != nil {
			return se.fail(err)
		}
		plan.Paydays = append(plan.Paydays, n)
	}
	if income != nil {
		plan.ExpectedIncome = income
	}
	if _, err := se.h.cfg.Budget.SetPlan(se.ctx, plan); err != nil {
		return se.fail(err)
	}
	se.h.cfg.Events.Track(se.ctx, se.u.TelegramID, events.BudgetSet, nil)
	return se.afterMoney()
}

func (se *session) afterMoney() string {
	if !se.isNew() {
		return done
	}
	return stepMorning
}

// morningChoices are the hours the onboarding offers; /settings has the rest.
var morningChoices = []int{8, 9, 10}

func askMorning(se *session) view {
	t := se.h.t
	hours := make([]models.InlineKeyboardButton, len(morningChoices))
	for i, h := range morningChoices {
		hours[i] = stepButton(hourLabel(h), stepMorning, strconv.Itoa(h))
	}
	return view{text: t.OnboardMorning, buttons: [][]models.InlineKeyboardButton{
		hours, row(stepButton(t.OnboardNoMorning, stepMorning, valueSkip)),
	}}
}

func pressMorning(se *session, value string) string {
	n := se.u.Notifications
	n.Reminders = false
	if hour, err := strconv.Atoi(value); err == nil {
		n.Reminders, n.ReminderHour = true, hour
	}
	if _, err := se.h.family.SetNotifications(se.ctx, se.u.TelegramID, n); err != nil {
		return se.fail(err)
	}
	return stepRecord
}

func askRecord(se *session) view {
	return view{text: se.h.t.OnboardRecord, buttons: [][]models.InlineKeyboardButton{skipRow(se, stepRecord)}}
}
