package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/access"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/card"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/game"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/report"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/startparam"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/tasks"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// FromCard marks the mini app button under a message that replaced a card.
const FromCard = "card"

func actionRows(rows [][]game.Action) [][]models.InlineKeyboardButton {
	out := make([][]models.InlineKeyboardButton, len(rows))
	for i, r := range rows {
		for _, a := range r {
			out[i] = append(out[i], button(a.Label, a.Data))
		}
	}
	return out
}

// SendActions delivers a message the bot starts itself, with its buttons; from
// tells the mini app which message the user came from.
func (h *Handler) SendActions(ctx context.Context, s Sender, chatID int64, text, from string, rows [][]game.Action) error {
	params := &bot.SendMessageParams{ChatID: chatID, Text: text}
	buttons := actionRows(rows)
	if h.cfg.MiniAppURL != "" {
		buttons = append(buttons, []models.InlineKeyboardButton{
			{Text: h.t.AppButton, WebApp: &models.WebAppInfo{URL: h.cfg.MiniAppURL + "?from=" + from}},
		})
	}
	if len(buttons) > 0 {
		params.ReplyMarkup = &models.InlineKeyboardMarkup{InlineKeyboard: buttons}
	}
	return h.sendMessage(ctx, s, params)
}

// referralLink is the user's link; empty if there is no way to build one.
func (h *Handler) referralLink(ctx context.Context, userID int64) string {
	if h.cfg.Game == nil || h.cfg.BotUsername == "" {
		return ""
	}
	code, err := h.cfg.Game.ReferralCode(ctx, userID)
	if err != nil {
		h.log.WarnContext(ctx, "referral code", "user_id", userID, "err", err)
		return ""
	}
	return "t.me/" + h.cfg.BotUsername + "?start=ref_" + code
}

// SendCard renders a card and sends it as a photo. The link goes on the
// picture too: in a story a caption is lost.
func (h *Handler) SendCard(ctx context.Context, s Sender, chatID int64, c card.Card, caption string, rows [][]game.Action) error {
	link := h.referralLink(ctx, chatID)
	c.Footer = link
	if link != "" {
		caption += "\n\n" + texts.Fill(h.t.ReferralLine, "link", "https://"+link)
	}
	data, err := card.Render(c)
	if err != nil {
		h.log.ErrorContext(ctx, "render card", "user_id", chatID, "err", err)
		return h.SendActions(ctx, s, chatID, caption, FromCard, rows) // the words still matter
	}
	h.cfg.Events.Track(ctx, chatID, events.CardGenerated, nil)
	params := &bot.SendPhotoParams{
		ChatID:  chatID,
		Photo:   &models.InputFileUpload{Filename: "savepoint.png", Data: bytes.NewReader(data)},
		Caption: caption,
	}
	if buttons := actionRows(rows); len(buttons) > 0 {
		params.ReplyMarkup = &models.InlineKeyboardMarkup{InlineKeyboard: buttons}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
	defer cancel()
	if _, err := s.SendPhoto(ctx, params); err != nil {
		if permanent(err) {
			return fmt.Errorf("%w: %w", ErrUndeliverable, err)
		}
		return err
	}
	return nil
}

// Quests.

func (h *Handler) gameOn() bool { return h.cfg.Game != nil }

func (h *Handler) userDay(u user.User) time.Time { return today(h.now(), u.Location(h.cfg.Location)) }

func (h *Handler) showQuests(ctx context.Context, s Sender, chatID int64, u user.User) {
	if !h.gameOn() {
		h.send(ctx, s, chatID, h.t.Help)
		return
	}
	day := h.userDay(u)
	p, err := h.cfg.Game.Profile(ctx, u.TelegramID)
	var list []game.Listed
	if err == nil {
		list, err = h.cfg.Game.Quests(ctx, u.TelegramID, day)
	}
	if err != nil {
		h.replyOrFail(ctx, s, chatID, 0, view{}, err)
		return
	}
	lines := []string{texts.Fill(h.t.QuestsHeader, "xp", strconv.Itoa(p.XP), "level", strconv.Itoa(p.Level),
		"name", report.LevelName(h.t, p.Level), "streak", report.Days(p.CurrentStreak(day)))}
	var buttons [][]models.InlineKeyboardButton
	for _, q := range list {
		line := h.t.QuestLine
		if q.Status != game.StatusOffered {
			line = h.t.QuestClosedLine
		} else if q.Quest.Check == game.CheckManual {
			buttons = append(buttons, row(button(texts.Fill(h.t.QuestListButton, "title", q.Quest.Title), game.ActQuest+":"+q.Quest.Slug)))
		}
		lines = append(lines, texts.Fill(line, "title", q.Quest.Title, "text", q.Quest.Text, "xp", strconv.Itoa(q.Quest.XP)))
	}
	if p.CurrentStreak(day) == 0 || !p.LastDay.Equal(day) {
		buttons = append(buttons, row(button(h.t.NoSpendButton, game.ActNoSpend)))
	}
	h.reply(ctx, s, chatID, 0, view{text: strings.Join(lines, "\n\n"), buttons: buttons})
}

func (h *Handler) questDone(ctx context.Context, s Sender, chatID int64, messageID int, u user.User, slug string) {
	if !h.gameOn() {
		return
	}
	d, err := h.cfg.Game.CompleteManual(ctx, u.TelegramID, slug, h.userDay(u), h.now())
	switch {
	case errors.Is(err, game.ErrUnknown):
		h.reply(ctx, s, chatID, messageID, view{text: h.t.FlowStale})
	case err != nil:
		h.replyOrFail(ctx, s, chatID, messageID, view{}, err)
	case !d.Awarded:
		h.reply(ctx, s, chatID, messageID, view{text: h.t.QuestAlready})
	default:
		h.cfg.Events.Track(ctx, u.TelegramID, events.QuestDone, events.Props{"quest": slug})
		h.reply(ctx, s, chatID, messageID, view{text: texts.Fill(h.t.QuestDone, "title", d.Title, "xp", strconv.Itoa(d.XP))})
	}
}

func (h *Handler) noSpend(ctx context.Context, s Sender, chatID int64, messageID int, u user.User) {
	if !h.gameOn() {
		return
	}
	day := h.userDay(u)
	mark, err := h.cfg.Game.Touch(ctx, u.TelegramID, day)
	if err != nil {
		h.replyOrFail(ctx, s, chatID, messageID, view{}, err)
		return
	}
	p, err := h.cfg.Game.Profile(ctx, u.TelegramID)
	if err != nil {
		h.replyOrFail(ctx, s, chatID, messageID, view{}, err)
		return
	}
	h.reply(ctx, s, chatID, messageID, view{text: texts.Fill(h.t.NoSpendDone, "days", report.Days(p.CurrentStreak(day)))})
	if mark != nil {
		caption := texts.Fill(h.t.StreakMark, "days", report.Days(mark.Days), "xp", strconv.Itoa(mark.XP))
		_ = h.SendCard(ctx, s, chatID, report.StreakCard(h.t, mark.Days), caption, nil)
	}
}

// taskDone answers the button of a reminder.
func (h *Handler) taskDone(ctx context.Context, s Sender, chatID int64, messageID int, u user.User, arg string) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || h.cfg.Tasks == nil {
		return
	}
	t, err := h.cfg.Tasks.Done(ctx, u.TelegramID, id, h.userDay(u), h.now())
	switch {
	case errors.Is(err, tasks.ErrNotFound):
		h.reply(ctx, s, chatID, messageID, view{text: h.t.FlowStale})
	case err != nil:
		h.replyOrFail(ctx, s, chatID, messageID, view{}, err)
	case t.GoalID != nil:
		h.reply(ctx, s, chatID, messageID, view{text: h.t.TaskDoneGoal})
	default:
		h.cfg.Events.Track(ctx, u.TelegramID, events.TaskDone, nil)
		h.reply(ctx, s, chatID, messageID, view{text: h.t.TaskDone})
	}
}

// Levels.

const (
	flowLevel = "level"
	stepLevel = "level"
	keyAsked  = "asked"
	valueYes  = "yes"
	valueNo   = "no"
)

func (h *Handler) showLevel(ctx context.Context, s Sender, chatID int64, u user.User) {
	if !h.gameOn() || !h.dialogsOn() {
		h.send(ctx, s, chatID, h.t.Help)
		return
	}
	p, err := h.cfg.Game.Profile(ctx, u.TelegramID)
	if err != nil {
		h.replyOrFail(ctx, s, chatID, 0, view{}, err)
		return
	}
	if !p.LevelTested && p.Level == 0 {
		h.begin(ctx, s, chatID, u, flowLevel, stepLevel, false)
		return
	}
	v := view{text: texts.Fill(h.t.LevelView, "level", strconv.Itoa(p.Level), "name", report.LevelName(h.t, p.Level),
		"xp", strconv.Itoa(p.XP), "next", h.levelNext(p.Level))}
	if next := p.Level + 1; game.SelfConfirmed(next) {
		v.buttons = [][]models.InlineKeyboardButton{row(button(texts.Fill(h.t.LevelConfirmButton, "name", report.LevelName(h.t, next)),
			game.ActLevelUp+":"+strconv.Itoa(next)))}
	}
	h.reply(ctx, s, chatID, 0, v)
}

func (h *Handler) levelNext(level int) string {
	if level >= len(h.t.LevelConditions) {
		return h.t.LevelTop
	}
	return h.t.LevelConditions[level]
}

func askLevel(se *session) view {
	t := se.h.t
	i, _ := strconv.Atoi(se.st.Data[keyAsked])
	return view{text: t.LevelQuestions[min(i, len(t.LevelQuestions)-1)], buttons: [][]models.InlineKeyboardButton{row(
		stepButton(t.LevelYes, stepLevel, valueYes), stepButton(t.LevelNo, stepLevel, valueNo),
	)}}
}

// pressLevel walks up the ladder: the level is the last "yes" in a row.
func pressLevel(se *session, value string) string {
	i, _ := strconv.Atoi(se.st.Data[keyAsked])
	if value == valueYes {
		i++
		if i < len(se.h.t.LevelQuestions) {
			se.st.Data[keyAsked] = strconv.Itoa(i)
			return stepLevel
		}
	}
	p, err := se.h.cfg.Game.ApplyTest(se.ctx, se.u.TelegramID, i)
	if err != nil {
		return se.fail(err)
	}
	se.h.dropFlow(se.ctx, se.u.TelegramID)
	se.show(view{text: texts.Fill(se.h.t.LevelResult, "level", strconv.Itoa(p.Level), "name", report.LevelName(se.h.t, p.Level),
		"next", se.h.levelNext(p.Level))})
	return stay
}

func (h *Handler) confirmLevel(ctx context.Context, s Sender, chatID int64, messageID int, u user.User, arg string) {
	level, err := strconv.Atoi(arg)
	if err != nil || !h.gameOn() {
		return
	}
	raised, err := h.cfg.Game.Confirm(ctx, u.TelegramID, level)
	if err != nil {
		h.replyOrFail(ctx, s, chatID, messageID, view{}, err)
		return
	}
	if !raised {
		h.reply(ctx, s, chatID, messageID, view{text: h.t.FlowStale})
		return
	}
	h.cfg.Events.Track(ctx, u.TelegramID, events.LevelUp, events.Props{"level": level})
	h.reply(ctx, s, chatID, messageID, view{text: texts.Fill(h.t.LevelUp, "level", arg, "name", report.LevelName(h.t, level))})
	_ = h.SendCard(ctx, s, chatID, report.LevelCard(h.t, level), texts.Fill(h.t.LevelUp, "level", arg, "name", report.LevelName(h.t, level)), nil)
}

// Challenges.

func (h *Handler) showChallenges(ctx context.Context, s Sender, chatID int64, u user.User) {
	if !h.gameOn() {
		h.send(ctx, s, chatID, h.t.Help)
		return
	}
	day := h.userDay(u)
	active, err := h.cfg.Game.Active(ctx, u.TelegramID, day)
	if err != nil {
		h.replyOrFail(ctx, s, chatID, 0, view{}, err)
		return
	}
	var lines []string
	var buttons [][]models.InlineKeyboardButton
	busy := map[string]bool{}
	for _, cur := range active {
		busy[cur.Challenge.Slug] = true
		task := h.t.ChallengeNotStarted
		if cur.Today != nil {
			task = cur.Today.Task
		}
		lines = append(lines, texts.Fill(h.t.ChallengeCurrent, "title", cur.Challenge.Title, "day", strconv.Itoa(cur.DayNo),
			"total", strconv.Itoa(len(cur.Challenge.Days)), "task", task))
		if cur.Today != nil && cur.Mark != game.DayDone {
			data := game.ActDay + ":" + cur.Challenge.Slug + ":" + strconv.Itoa(cur.DayNo) + ":" + game.DayDone
			buttons = append(buttons, row(button(h.t.ChallengeDoneButton, data)))
		}
		buttons = append(buttons, row(button(h.t.ChallengeLeaveButton, game.ActLeave+":"+cur.Challenge.Slug)))
	}
	for _, c := range h.cfg.Game.Catalog().Challenges {
		if busy[c.Slug] || !c.Open(day) {
			continue
		}
		lines = append(lines, texts.Fill(h.t.ChallengeItem, "title", c.Title, "description", c.Description, "days", report.Days(len(c.Days))))
		buttons = append(buttons, row(button(texts.Fill(h.t.ChallengeJoinButton, "title", c.Title), game.ActJoin+":"+c.Slug)))
	}
	if len(lines) == 0 {
		lines = []string{h.t.ChallengeNone}
	}
	h.reply(ctx, s, chatID, 0, view{text: strings.Join(lines, "\n\n"), buttons: buttons})
}

// offerChallenge answers a link to a challenge.
func (h *Handler) offerChallenge(ctx context.Context, s Sender, chatID int64, slug string) bool {
	c, ok := h.cfg.Game.Catalog().Challenge(slug)
	if !ok {
		return false
	}
	h.reply(ctx, s, chatID, 0, view{
		text:    texts.Fill(h.t.ChallengeItem, "title", c.Title, "description", c.Description, "days", report.Days(len(c.Days))),
		buttons: [][]models.InlineKeyboardButton{row(button(texts.Fill(h.t.ChallengeJoinButton, "title", c.Title), game.ActJoin+":"+c.Slug))},
	})
	return true
}

func (h *Handler) joinChallenge(ctx context.Context, s Sender, chatID int64, messageID int, u user.User, slug string) {
	if !h.gameOn() {
		return
	}
	day := h.userDay(u)
	c, joined, err := h.cfg.Game.Join(ctx, slug, u.TelegramID, day)
	switch {
	case errors.Is(err, game.ErrUnknown):
		h.reply(ctx, s, chatID, messageID, view{text: h.t.FlowStale})
	case errors.Is(err, game.ErrClosed):
		h.reply(ctx, s, chatID, messageID, view{text: h.t.ChallengeClosed})
	case errors.Is(err, game.ErrClubOnly):
		h.reply(ctx, s, chatID, messageID, view{text: h.t.ChallengeClubOnly})
	case err != nil:
		h.replyOrFail(ctx, s, chatID, messageID, view{}, err)
	case !joined:
		h.reply(ctx, s, chatID, messageID, view{text: h.t.ChallengeAlready})
	default:
		h.cfg.Events.Track(ctx, u.TelegramID, events.ChallengeJoined, events.Props{"challenge": slug})
		h.reply(ctx, s, chatID, messageID, view{text: texts.Fill(h.t.ChallengeJoined, "title", c.Title,
			"date", agent.HumanDate(c.StartFor(day), day), "hour", hourLabel(u.ReminderHour))})
	}
}

func (h *Handler) leaveChallenge(ctx context.Context, s Sender, chatID int64, messageID int, u user.User, slug string) {
	if !h.gameOn() {
		return
	}
	err := h.cfg.Game.Leave(ctx, slug, u.TelegramID)
	h.replyOrFail(ctx, s, chatID, messageID, view{text: h.t.ChallengeLeft}, err)
}

// markTask answers the buttons of a task, "<slug>:<day>:<status>".
func (h *Handler) markTask(ctx context.Context, s Sender, chatID int64, messageID int, u user.User, arg string) {
	parts := strings.Split(arg, ":")
	if len(parts) != 3 || !h.gameOn() {
		return
	}
	dayNo, err := strconv.Atoi(parts[1])
	status := parts[2]
	if err != nil || status != game.DayDone && status != game.DayLater && status != game.DaySkipped {
		return
	}
	d, err := h.cfg.Game.MarkDay(ctx, parts[0], u.TelegramID, dayNo, status, h.userDay(u))
	switch {
	case errors.Is(err, game.ErrUnknown):
		h.reply(ctx, s, chatID, messageID, view{text: h.t.FlowStale})
	case err != nil:
		h.replyOrFail(ctx, s, chatID, messageID, view{}, err)
	case status == game.DayLater:
		h.reply(ctx, s, chatID, messageID, view{text: h.t.ChallengeLaterOK})
	case status == game.DaySkipped:
		h.reply(ctx, s, chatID, messageID, view{text: h.t.ChallengeSkipped})
	case !d.Awarded:
		h.reply(ctx, s, chatID, messageID, view{text: h.t.QuestAlready})
	default:
		h.cfg.Events.Track(ctx, u.TelegramID, events.ChallengeDayDone, events.Props{"challenge": parts[0], "day": dayNo})
		h.reply(ctx, s, chatID, messageID, view{text: texts.Fill(h.t.QuestDone, "title", d.Title, "xp", strconv.Itoa(d.XP))})
	}
}

// Referrals and cards.

// landOnReferral notes who brought a new user.
func (h *Handler) landOnReferral(ctx context.Context, u user.User, p startparam.Param) {
	if !h.gameOn() || p.Kind != startparam.Referral {
		return
	}
	if err := h.cfg.Game.Refer(ctx, p.Ref, u.TelegramID); err != nil {
		h.log.ErrorContext(ctx, "referral", "user_id", u.TelegramID, "err", err)
		return
	}
	h.cfg.Events.Track(ctx, u.TelegramID, events.ReferralJoined, nil)
}

// rewardReferral pays for a friend who has finished onboarding.
func (h *Handler) rewardReferral(ctx context.Context, s Sender, u user.User) {
	if !h.gameOn() {
		return
	}
	referrer, friends, err := h.cfg.Game.RewardReferrer(ctx, u.TelegramID)
	if err != nil {
		h.log.ErrorContext(ctx, "referral reward", "user_id", u.TelegramID, "err", err)
		return
	}
	if referrer == 0 {
		return
	}
	h.notify(ctx, s, referrer, h.t.ReferralRewarded)
	if h.cfg.Access == nil || friends%access.ReferralsForClub != 0 {
		return
	}
	if _, err := h.cfg.Access.Grant(ctx, referrer, access.SourceReferral, access.ReferralDays); err != nil {
		h.log.ErrorContext(ctx, "referral club", "user_id", referrer, "err", err)
		return
	}
	h.notify(ctx, s, referrer, h.t.ReferralClub)
}

// goalCard sends the card of a goal again, now with the amounts.
func (h *Handler) goalCard(ctx context.Context, s Sender, chatID int64, u user.User, arg string) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || h.cfg.Budget == nil {
		return
	}
	g, err := h.cfg.Budget.Goal(ctx, u.TelegramID, id)
	if err != nil {
		h.send(ctx, s, chatID, h.t.FlowStale)
		return
	}
	if err := h.SendCard(ctx, s, chatID, report.GoalCard(h.t, g, true), g.Title, nil); err != nil {
		h.log.WarnContext(ctx, "goal card", "user_id", u.TelegramID, "err", err)
	}
}

// Owner.

func (h *Handler) challengeStats(ctx context.Context, s Sender, chatID int64, slug string) {
	if !h.gameOn() {
		h.send(ctx, s, chatID, h.t.Failure)
		return
	}
	if slug == "" {
		var slugs []string
		for _, c := range h.cfg.Game.Catalog().Challenges {
			slugs = append(slugs, c.Slug)
		}
		h.send(ctx, s, chatID, texts.Fill(h.t.AdminChallengeUsage, "slugs", strings.Join(slugs, ", ")))
		return
	}
	st, worst, err := h.cfg.Game.Stats(ctx, slug)
	if errors.Is(err, game.ErrUnknown) {
		h.send(ctx, s, chatID, h.t.AdminChallengeUnknown)
		return
	}
	if err != nil {
		h.log.ErrorContext(ctx, "challenge stats", "err", err)
		h.send(ctx, s, chatID, h.t.Failure)
		return
	}
	h.send(ctx, s, chatID, texts.Fill(h.t.AdminChallenge, "slug", slug, "participants", strconv.Itoa(st.Participants),
		"finished", strconv.Itoa(st.Finished), "worst", strconv.Itoa(worst)))
}
