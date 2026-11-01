package telegram

import (
	"bytes"
	"context"
	"encoding/csv"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/quick"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// The request for an anonymous review in a video. The answers are stored
// without a name, and the export leaves out who sent them.

const (
	flowReview = "review"

	stepReviewStart    = "rstart"
	stepReviewAge      = "rage"
	stepReviewIncome   = "rincome"
	stepReviewExpenses = "rspend"
	stepReviewDebts    = "rdebts"
	stepReviewRate     = "rrate"
	stepReviewSavings  = "rsaved"
	stepReviewGoal     = "rgoal"
	stepReviewQuestion = "rquest"
	stepReviewConsent  = "rconsent"

	keyAge      = "age"
	keyIncome   = "income"
	keyExpenses = "expenses"
	keyDebts    = "debts"
	keyRate     = "rate"
	keySavings  = "savings"
	keyGoal     = "goal"
	keyQuestion = "question"

	maxReviewQuestion = 500 // runes
	maxReviewGoal     = 100
	maxRate           = 1000

	valueMine = "mine"
	valueNone = "none"
)

// reviewColumns is the export, in this order. Nothing here says who it is.
var reviewColumns = []string{keyAge, keyIncome, keyExpenses, keyDebts, keyRate, keySavings, keyGoal, keyQuestion}

func (h *Handler) startReview(ctx context.Context, s Sender, chatID int64, u user.User) {
	if !h.dialogsOn() || !h.gameOn() {
		h.send(ctx, s, chatID, h.t.Help)
		return
	}
	h.begin(ctx, s, chatID, u, flowReview, stepReviewStart, false)
}

func askReviewStart(se *session) view {
	t := se.h.t
	return view{text: t.ReviewIntro, buttons: [][]models.InlineKeyboardButton{
		row(stepButton(t.ReviewFromData, stepReviewStart, valueMine)),
		row(stepButton(t.ReviewManual, stepReviewStart, valueNone)),
	}}
}

// pressReviewStart fills in what the bot already knows.
func pressReviewStart(se *session, value string) string {
	if value == valueMine {
		if err := se.fillReview(); err != nil {
			return se.fail(err)
		}
	}
	return stepReviewAge
}

func (se *session) fillReview() error {
	ctx, id := se.ctx, se.u.TelegramID
	b := se.h.cfg.Budget
	plan, err := b.Plan(ctx, id)
	if err != nil {
		return err
	}
	if plan != nil {
		if income := plan.MonthlyIncome(); income != nil {
			se.st.Data[keyIncome] = income.String()
		}
	}
	payments, err := b.Payments(ctx, id)
	if err != nil {
		return err
	}
	if len(payments) > 0 {
		sum := decimal.Zero
		for _, p := range payments {
			sum = sum.Add(p.Amount)
		}
		se.st.Data[keyExpenses] = sum.String()
	}
	goals, err := b.Goals(ctx, id)
	if err != nil {
		return err
	}
	if len(goals) > 0 {
		sum := decimal.Zero
		for _, g := range goals {
			sum = sum.Add(g.Saved)
		}
		se.st.Data[keySavings] = sum.String()
	}
	if g := budget.MainGoal(goals); g != nil {
		se.st.Data[keyGoal] = g.Title
	}
	return nil
}

func askReviewAge(se *session) view {
	t := se.h.t
	var rows [][]models.InlineKeyboardButton
	for i, label := range t.ReviewAges {
		b := stepButton(label, stepReviewAge, strconv.Itoa(i))
		if i%2 == 0 {
			rows = append(rows, row(b))
		} else {
			rows[len(rows)-1] = append(rows[len(rows)-1], b)
		}
	}
	return view{text: t.ReviewAge, buttons: rows}
}

func pressReviewAge(se *session, value string) string {
	i, err := strconv.Atoi(value)
	if err != nil || i < 0 || i >= len(se.h.t.ReviewAges) {
		return again
	}
	se.st.Data[keyAge] = se.h.t.ReviewAges[i]
	return se.nextReview(stepReviewAge)
}

// reviewOrder is the questions in order; those answered from the user's data
// are not asked.
var reviewOrder = []struct{ step, key string }{
	{stepReviewIncome, keyIncome},
	{stepReviewExpenses, keyExpenses},
	{stepReviewDebts, keyDebts},
	{stepReviewRate, keyRate},
	{stepReviewSavings, keySavings},
	{stepReviewGoal, keyGoal},
	{stepReviewQuestion, keyQuestion},
}

func (se *session) nextReview(after string) string {
	seen := after == stepReviewAge
	for _, q := range reviewOrder {
		if seen && se.st.Data[q.key] == "" {
			return q.step
		}
		seen = seen || q.step == after
	}
	return stepReviewConsent
}

func exitRow(se *session) []models.InlineKeyboardButton {
	return row(button(se.h.t.ExitButton, cbFlow+":"+stepExit))
}

func amountStep(text func(texts.Bot) string, step, key string) (func(*session) view, func(*session, string) string) {
	ask := func(se *session) view {
		return view{text: text(se.h.t), buttons: [][]models.InlineKeyboardButton{exitRow(se)}}
	}
	answer := func(se *session, input string) string {
		amount, ok := quick.Amount(input)
		if !ok {
			return again
		}
		se.st.Data[key] = amount.String()
		return se.nextReview(step)
	}
	return ask, answer
}

// trimPercent reads "24,9 %" as a number.
func trimPercent(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%")), ",", ".")
	return s
}

func askReviewDebts(se *session) view {
	return view{text: se.h.t.ReviewDebts, buttons: [][]models.InlineKeyboardButton{
		row(stepButton(se.h.t.ReviewNoDebts, stepReviewDebts, valueNone)), exitRow(se),
	}}
}

func textReviewDebts(se *session, input string) string {
	amount, ok := quick.Amount(input)
	if !ok {
		return again
	}
	se.st.Data[keyDebts] = amount.String()
	return stepReviewRate
}

func pressReviewDebts(se *session, _ string) string {
	se.st.Data[keyDebts], se.st.Data[keyRate] = "0", "0"
	return se.nextReview(stepReviewRate)
}

func askReviewRate(se *session) view {
	return view{text: se.h.t.ReviewRate, buttons: [][]models.InlineKeyboardButton{exitRow(se)}}
}

func textReviewRate(se *session, input string) string {
	rate, err := decimal.NewFromString(trimPercent(input))
	if err != nil || rate.IsNegative() || rate.GreaterThan(decimal.NewFromInt(maxRate)) {
		return again
	}
	se.st.Data[keyRate] = rate.String()
	return se.nextReview(stepReviewRate)
}

func askReviewGoal(se *session) view {
	return view{text: se.h.t.ReviewGoal, buttons: [][]models.InlineKeyboardButton{exitRow(se)}}
}

func textReviewGoal(se *session, input string) string {
	if input == "" || utf8.RuneCountInString(input) > maxReviewGoal {
		return again
	}
	se.st.Data[keyGoal] = input
	return se.nextReview(stepReviewGoal)
}

func askReviewQuestion(se *session) view {
	return view{text: se.h.t.ReviewQuestion, buttons: [][]models.InlineKeyboardButton{exitRow(se)}}
}

func textReviewQuestion(se *session, input string) string {
	if input == "" || utf8.RuneCountInString(input) > maxReviewQuestion {
		return again
	}
	se.st.Data[keyQuestion] = input
	return stepReviewConsent
}

func askReviewConsent(se *session) view {
	return view{text: se.h.t.ReviewConsent, buttons: [][]models.InlineKeyboardButton{
		row(stepButton(se.h.t.ReviewConsentButton, stepReviewConsent, valueYes)), exitRow(se),
	}}
}

// pressReviewConsent keeps the answers only now: without the consent nothing
// stays once the dialog is gone.
func pressReviewConsent(se *session, _ string) string {
	answers := map[string]string{}
	for _, key := range reviewColumns {
		answers[key] = se.st.Data[key]
	}
	if err := se.h.cfg.Game.AddReview(se.ctx, se.u.TelegramID, answers, se.h.now()); err != nil {
		return se.fail(err)
	}
	se.h.cfg.Events.Track(se.ctx, se.u.TelegramID, events.ReviewGiven, nil)
	se.h.dropFlow(se.ctx, se.u.TelegramID)
	se.show(view{text: se.h.t.ReviewThanks})
	if se.h.cfg.AdminID != 0 {
		se.h.notify(se.ctx, se.s, se.h.cfg.AdminID, se.h.t.AdminReviewNew)
	}
	return stay
}

// reviewsCSV is the export for preparing a video.
func reviewsCSV(answers []map[string]string) ([]byte, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	if err := w.Write(reviewColumns); err != nil {
		return nil, err
	}
	for _, a := range answers {
		row := make([]string, len(reviewColumns))
		for i, key := range reviewColumns {
			row[i] = a[key]
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return b.Bytes(), w.Error()
}

// showReviews tells the owner how many requests are new and sends all of them.
func (h *Handler) showReviews(ctx context.Context, s Sender, chatID int64) {
	if !h.gameOn() {
		h.send(ctx, s, chatID, h.t.Failure)
		return
	}
	reviews, err := h.cfg.Game.Reviews(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "list reviews", "err", err)
		h.send(ctx, s, chatID, h.t.Failure)
		return
	}
	if len(reviews) == 0 {
		h.send(ctx, s, chatID, h.t.AdminNoReviews)
		return
	}
	fresh := 0
	answers := make([]map[string]string, len(reviews))
	for i, r := range reviews {
		answers[i] = r.Answers
		if r.Status == "new" {
			fresh++
		}
	}
	data, err := reviewsCSV(answers)
	if err != nil {
		h.log.ErrorContext(ctx, "export reviews", "err", err)
		h.send(ctx, s, chatID, h.t.Failure)
		return
	}
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
	defer cancel()
	_, err = s.SendDocument(sendCtx, &bot.SendDocumentParams{
		ChatID:   chatID,
		Document: &models.InputFileUpload{Filename: "reviews-" + h.now().Format(time.DateOnly) + ".csv", Data: bytes.NewReader(data)},
		Caption:  texts.Fill(h.t.AdminReviews, "new", strconv.Itoa(fresh), "total", strconv.Itoa(len(reviews))),
	})
	if err != nil {
		h.log.ErrorContext(ctx, "send reviews", "err", err)
		h.send(ctx, s, chatID, h.t.Failure)
		return
	}
	if err := h.cfg.Game.MarkReviewsSeen(ctx); err != nil {
		h.log.ErrorContext(ctx, "mark reviews", "err", err)
	}
}
