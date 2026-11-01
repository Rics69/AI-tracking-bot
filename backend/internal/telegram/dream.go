package telegram

import (
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/dream"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/quick"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/report"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
)

// The dream calculator: what a goal costs per month and per day.

const (
	stepDreamTitle  = "dtitle"
	stepDreamPrice  = "dprice"
	stepDreamTerm   = "dterm"
	stepDreamDate   = "ddate"
	stepDreamSaved  = "dsaved"
	stepDreamResult = "dresult"

	valueCustom = "custom"
	valueZero   = "zero"
	valueGoal   = "goal"
	valueBoost  = "boost"
)

// termMonths go with the labels of dream_terms in texts.yaml.
var termMonths = []int{6, 12, 24, 36}

func askDreamTitle(se *session) view {
	t := se.h.t
	var rows [][]models.InlineKeyboardButton
	for i, label := range t.DreamPresets {
		b := stepButton(label, stepDreamTitle, strconv.Itoa(i))
		if i%2 == 0 {
			rows = append(rows, row(b))
		} else {
			rows[len(rows)-1] = append(rows[len(rows)-1], b)
		}
	}
	return view{text: t.DreamTitle, buttons: append(rows, se.leaveRow(stepDreamTitle))}
}

// leaveRow is the way out of a question: in onboarding it is skipped like any
// other step, someone who came for the calculator just leaves.
func (se *session) leaveRow(name string) []models.InlineKeyboardButton {
	if se.isNew() {
		return skipRow(se, name)
	}
	return row(button(se.h.t.ExitButton, cbFlow+":"+stepExit))
}

func textDreamTitle(se *session, text string) string {
	if !validTitle(text) {
		return again
	}
	se.st.Data[keyTitle] = text
	return stepDreamPrice
}

func pressDreamTitle(se *session, value string) string {
	if value == valueSkip {
		return se.afterGoal()
	}
	i, err := strconv.Atoi(value)
	if err != nil || i < 0 || i >= len(se.h.t.DreamPresets) {
		return again
	}
	se.st.Data[keyTitle] = se.h.t.DreamPresets[i]
	return stepDreamPrice
}

func askDreamPrice(se *session) view {
	return view{text: se.h.t.DreamPrice, buttons: [][]models.InlineKeyboardButton{se.leaveRow(stepDreamPrice)}}
}

func textDreamPrice(se *session, text string) string {
	amount, ok := quick.Amount(text)
	if !ok {
		return again
	}
	se.st.Data[keyPrice] = amount.String()
	return stepDreamTerm
}

func askDreamTerm(se *session) view {
	t := se.h.t
	terms := make([]models.InlineKeyboardButton, 0, len(termMonths))
	for i, months := range termMonths {
		if i < len(t.DreamTerms) {
			terms = append(terms, stepButton(t.DreamTerms[i], stepDreamTerm, strconv.Itoa(months)))
		}
	}
	return view{text: t.DreamTerm, buttons: [][]models.InlineKeyboardButton{
		terms, row(stepButton(t.DreamCustomDate, stepDreamTerm, valueCustom)), se.leaveRow(stepDreamTerm),
	}}
}

func pressDreamTerm(se *session, value string) string {
	switch value {
	case valueCustom:
		return stepDreamDate
	case valueSkip:
		return se.afterGoal()
	}
	months, err := strconv.Atoi(value)
	if err != nil {
		return again
	}
	se.st.Data[keyDeadline] = se.day.AddDate(0, months, 0).Format(time.DateOnly)
	return stepDreamSaved
}

func askDreamDate(se *session) view {
	return view{text: se.h.t.DreamDate, buttons: [][]models.InlineKeyboardButton{se.leaveRow(stepDreamDate)}}
}

func textDreamDate(se *session, text string) string {
	d, ok := dream.ParseDeadline(text, se.day)
	if !ok {
		return again
	}
	se.st.Data[keyDeadline] = d.Format(time.DateOnly)
	return stepDreamSaved
}

func askDreamSaved(se *session) view {
	return view{text: se.h.t.DreamSaved, buttons: [][]models.InlineKeyboardButton{
		row(stepButton(se.h.t.DreamZero, stepDreamSaved, valueZero)),
	}}
}

func textDreamSaved(se *session, text string) string {
	amount, ok := quick.Amount(text)
	if !ok {
		return again
	}
	se.st.Data[keySaved] = amount.String()
	return se.calculated()
}

func pressDreamSaved(se *session, _ string) string { return se.calculated() }

func (se *session) calculated() string {
	se.h.cfg.Events.Track(se.ctx, se.u.TelegramID, events.DreamCalculated, nil)
	return stepDreamResult
}

// dreamInput rebuilds the calculation from the answers; the monthly income
// comes from the plan.
func (se *session) dreamInput() (dream.Input, time.Time, error) {
	price, err := decimal.NewFromString(se.st.Data[keyPrice])
	if err != nil {
		return dream.Input{}, time.Time{}, err
	}
	saved := decimal.Zero
	if v := se.st.Data[keySaved]; v != "" {
		if saved, err = decimal.NewFromString(v); err != nil {
			return dream.Input{}, time.Time{}, err
		}
	}
	deadline, err := time.Parse(time.DateOnly, se.st.Data[keyDeadline])
	if err != nil {
		return dream.Input{}, time.Time{}, err
	}
	in := dream.Input{Price: price, Saved: saved, Months: dream.Months(se.day, deadline)}
	plan, err := se.h.cfg.Budget.Plan(se.ctx, se.u.TelegramID)
	if err != nil {
		return dream.Input{}, time.Time{}, err
	}
	if plan != nil {
		in.MonthlyIncome = plan.MonthlyIncome()
	}
	if in.MonthlyIncome != nil && se.st.Data[keyBoost] != "" {
		boosted := in.MonthlyIncome.Mul(decimal.RequireFromString("1.1"))
		in.MonthlyIncome = &boosted
	}
	return in, deadline, nil
}

func askDreamResult(se *session) view {
	t := se.h.t
	in, deadline, err := se.dreamInput()
	if err != nil {
		se.h.log.ErrorContext(se.ctx, "dream result", "user_id", se.u.TelegramID, "err", err)
		return view{text: t.Failure}
	}
	title := se.st.Data[keyTitle]
	r := dream.Compute(in)
	buttons := row(stepButton(t.DreamMakeGoal, stepDreamResult, valueGoal))
	later := row(stepButton(t.DreamLater, stepDreamResult, valueSkip))
	if r.Done {
		return view{
			text:    texts.Fill(t.DreamAllThere, "title", title, "price", report.Money(in.Price)),
			buttons: [][]models.InlineKeyboardButton{later},
		}
	}
	lines := []string{texts.Fill(t.DreamResult, "title", title, "price", report.Money(in.Price),
		"deadline", agent.HumanDate(deadline, se.day), "month", report.Money(r.PerMonth), "day", report.Money(r.PerDay))}
	if r.Verdict != dream.Unknown {
		lines = append(lines, texts.Fill(t.DreamShare, "share", strconv.Itoa(r.Share), "verdict", verdict(t, r.Verdict)))
	}
	boosted := se.st.Data[keyBoost] != ""
	if boosted {
		lines = append(lines, t.DreamBoostNote)
	}
	if r.Verdict != dream.Unknown && !boosted {
		buttons = append(buttons, stepButton(t.DreamBoostButton, stepDreamResult, valueBoost))
	}
	return view{text: strings.Join(lines, "\n"), buttons: [][]models.InlineKeyboardButton{buttons, later}}
}

func verdict(t texts.Bot, v dream.Verdict) string {
	switch v {
	case dream.Realistic:
		return t.DreamRealistic
	case dream.Ambitious:
		return t.DreamAmbitious
	default:
		return t.DreamStretch
	}
}

func pressDreamResult(se *session, value string) string {
	switch value {
	case valueBoost:
		se.st.Data[keyBoost] = "1"
		return stepDreamResult
	case valueGoal:
		return se.makeGoal()
	default:
		return se.afterGoal()
	}
}

// makeGoal turns the calculation into a goal, what is saved already being its
// first deposit: the same as the set_goal tool and the mini app do.
func (se *session) makeGoal() string {
	in, deadline, err := se.dreamInput()
	if err != nil {
		return se.fail(err)
	}
	title := se.st.Data[keyTitle]
	g, err := se.createGoal(title, in.Price, &deadline)
	if err != nil {
		return se.fail(err)
	}
	if in.Saved.IsPositive() {
		if _, err := se.h.cfg.Budget.Deposit(se.ctx, se.u.TelegramID, g.ID, in.Saved, nil, se.day); err != nil {
			return se.fail(err)
		}
	}
	se.show(view{text: texts.Fill(se.h.t.DreamGoalSaved, "title", title)})
	return se.afterGoal()
}
