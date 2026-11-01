package telegram

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/game"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/startparam"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// Callback data of inline buttons: an action and an optional argument
// ("join:<code>", "kick:<id>"), at most 64 bytes. It comes from the client,
// so arguments are checked again like any input.
const (
	cbConsent  = "consent"  // [:invite code] (the code only on old messages)
	cbJoin     = "join"     // :invite code
	cbDecline  = "decline"  // stay out of the family
	cbFamily   = "family"   // show the family
	cbInvite   = "invite"   // create an invite link
	cbLeave    = "leave"    // ask to leave the family
	cbLeaveYes = "leave!"   // leave
	cbKick     = "kick"     // :member ID, ask to remove them
	cbKickYes  = "kick!"    // :member ID, remove
	cbSettings = "settings" // show settings
	cbWeekly   = "weekly"   // :on|off
	cbTimezone = "tz"       // [:IANA name]
	cbDelete   = "delete!"  // delete the account
	cbCancel   = "cancel"

	cbFlow   = "fl"     // :step:value of a dialog
	cbNotify = "notify" // :morning|rates|goals:on|off
	cbHour   = "hour"   // [:hour of the morning message]

	cbFix       = "fix"  // :operation ID, how to correct a quick record
	cbFixDelete = "fix!" // :operation ID, delete it
)

// view is a message with optional inline buttons.
type view struct {
	text    string
	buttons [][]models.InlineKeyboardButton
}

func button(text, data string) models.InlineKeyboardButton {
	return models.InlineKeyboardButton{Text: text, CallbackData: data}
}

func row(buttons ...models.InlineKeyboardButton) []models.InlineKeyboardButton { return buttons }

// reply sends v, or replaces the message messageID with it (after a button press).
func (h *Handler) reply(ctx context.Context, s Sender, chatID int64, messageID int, v view) {
	var markup models.ReplyMarkup
	if len(v.buttons) > 0 {
		markup = &models.InlineKeyboardMarkup{InlineKeyboard: v.buttons}
	}
	noPreview := true
	if messageID != 0 {
		_, err := s.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID: chatID, MessageID: messageID, Text: v.text, ReplyMarkup: markup,
			LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: &noPreview},
		})
		if err == nil {
			return
		}
		h.log.WarnContext(ctx, "edit message, sending a new one", "chat_id", chatID, "err", err)
	}
	_ = h.sendMessage(ctx, s, &bot.SendMessageParams{
		ChatID: chatID, Text: v.text, ReplyMarkup: markup,
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: &noPreview},
	})
}

func (h *Handler) handleCallback(ctx context.Context, s Sender, cq *models.CallbackQuery) {
	// Stop the spinner on the button whatever happens next.
	defer func() {
		_, _ = s.AnswerCallbackQuery(context.WithoutCancel(ctx), &bot.AnswerCallbackQueryParams{CallbackQueryID: cq.ID})
	}()
	chatID, messageID := cq.From.ID, 0 // buttons live in the private chat with the user
	if m := cq.Message.Message; m != nil {
		chatID, messageID = m.Chat.ID, m.ID
	}

	unlock := h.lock(cq.From.ID)
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, h.cfg.ReplyTimeout)
	defer cancel()

	u, ok := h.admit(ctx, s, chatID, &cq.From)
	if !ok {
		return
	}
	action, arg, _ := strings.Cut(cq.Data, ":")
	if action == cbConsent {
		h.consent(ctx, s, chatID, messageID, u, arg)
		return
	}
	if !u.Consented() {
		h.askConsent(ctx, s, chatID)
		return
	}

	switch action {
	case cbJoin:
		h.join(ctx, s, chatID, messageID, u, arg)
	case cbDecline:
		h.reply(ctx, s, chatID, messageID, view{text: h.t.Declined})
	case cbFamily:
		v, err := h.familyView(ctx, u)
		h.replyOrFail(ctx, s, chatID, messageID, v, err)
	case cbInvite:
		h.createInvite(ctx, s, chatID, messageID, u)
	case cbLeave:
		h.confirmLeave(ctx, s, chatID, messageID, u)
	case cbLeaveYes:
		h.leave(ctx, s, chatID, messageID, u)
	case cbKick, cbKickYes:
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return
		}
		if action == cbKick {
			h.confirmKick(ctx, s, chatID, messageID, u, id)
		} else {
			h.kick(ctx, s, chatID, messageID, u, id)
		}
	case cbSettings:
		h.reply(ctx, s, chatID, messageID, h.settingsView(u))
	case cbWeekly:
		updated, err := h.family.SetWeeklyReport(ctx, u, u.TelegramID, arg == "on")
		h.replyOrFail(ctx, s, chatID, messageID, h.settingsView(updated), err)
	case cbTimezone:
		if arg == "" {
			h.reply(ctx, s, chatID, messageID, h.timezoneView())
			return
		}
		updated, err := h.family.SetTimezone(ctx, u.TelegramID, arg)
		h.replyOrFail(ctx, s, chatID, messageID, h.settingsView(updated), err)
	case cbDelete:
		h.deleteAccount(ctx, s, chatID, messageID, u)
	case cbCancel:
		h.reply(ctx, s, chatID, messageID, view{text: h.t.Cancelled})
	case cbNotify:
		name, value, _ := strings.Cut(arg, ":")
		h.toggleNotification(ctx, s, chatID, messageID, u, name, value == "on")
	case cbHour:
		if arg == "" {
			h.reply(ctx, s, chatID, messageID, h.hourView())
			return
		}
		h.setHour(ctx, s, chatID, messageID, u, arg)
	case cbFlow:
		h.flowPress(ctx, s, chatID, messageID, u, arg)
	case game.ActQuest:
		h.questDone(ctx, s, chatID, messageID, u, arg)
	case game.ActNoSpend:
		h.noSpend(ctx, s, chatID, messageID, u)
	case game.ActDay:
		h.markTask(ctx, s, chatID, messageID, u, arg)
	case game.ActJoin:
		h.joinChallenge(ctx, s, chatID, messageID, u, arg)
	case game.ActLeave:
		h.leaveChallenge(ctx, s, chatID, messageID, u, arg)
	case game.ActLevelUp:
		h.confirmLevel(ctx, s, chatID, messageID, u, arg)
	case game.ActTaskDone:
		h.taskDone(ctx, s, chatID, messageID, u, arg)
	case game.ActGoalCard:
		h.goalCard(ctx, s, chatID, u, arg)
	case cbFix, cbFixDelete:
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return
		}
		if action == cbFix {
			h.quickFix(ctx, s, chatID, id)
		} else {
			h.quickDelete(ctx, s, chatID, messageID, u, id)
		}
	default:
		h.log.WarnContext(ctx, "unknown callback", "user_id", u.TelegramID, "data", cq.Data)
	}
}

func (h *Handler) replyOrFail(ctx context.Context, s Sender, chatID int64, messageID int, v view, err error) {
	if err != nil {
		h.log.ErrorContext(ctx, "callback failed", "chat_id", chatID, "err", err)
		v = view{text: h.t.Failure}
	}
	h.reply(ctx, s, chatID, messageID, v)
}

// Consent.

func (h *Handler) askConsent(ctx context.Context, s Sender, chatID int64) {
	policy := "/privacy"
	if h.cfg.PrivacyURL != "" {
		policy = h.cfg.PrivacyURL
	}
	h.reply(ctx, s, chatID, 0, view{
		text:    texts.Fill(h.t.Consent, "policy", policy),
		buttons: [][]models.InlineKeyboardButton{row(button(h.t.ConsentButton, cbConsent))},
	})
}

func (h *Handler) consent(ctx context.Context, s Sender, chatID int64, messageID int, u user.User, oldCode string) {
	first := !u.Consented()
	if first {
		if _, err := h.family.Consent(ctx, u.TelegramID); err != nil {
			h.replyOrFail(ctx, s, chatID, messageID, view{}, err)
			return
		}
		h.log.InfoContext(ctx, "user consented", "user_id", u.TelegramID)
	}
	raw := u.PendingStart // what the user came with, kept until now
	if raw == "" {
		raw = oldCode
	}
	p := h.landing(ctx, raw)
	if first || raw != "" {
		h.arrive(ctx, u, p, events.Consented)
	}
	h.reply(ctx, s, chatID, messageID, view{text: h.t.Welcome})
	if p.Kind == startparam.Invite {
		h.offerInvitation(ctx, s, chatID, p.Raw) // shown once the user has agreed
		return
	}
	if first {
		h.route(ctx, s, chatID, u, p, true)
	}
}

func (h *Handler) privacyText() string {
	if h.cfg.PrivacyURL == "" {
		return h.t.Privacy
	}
	return h.t.Privacy + "\n\n" + texts.Fill(h.t.PrivacyLink, "link", h.cfg.PrivacyURL)
}

// Family.

// offerInvitation shows who invites the user and asks before joining:
// the head will see all of the user's expenses.
func (h *Handler) offerInvitation(ctx context.Context, s Sender, chatID int64, code string) {
	head, err := h.family.Invitation(ctx, code)
	switch {
	case errors.Is(err, family.ErrInviteInvalid):
		h.send(ctx, s, chatID, h.t.InviteInvalid)
		return
	case err != nil:
		h.replyOrFail(ctx, s, chatID, 0, view{}, err)
		return
	}
	h.reply(ctx, s, chatID, 0, view{
		text: texts.Fill(h.t.Invitation, "head", head.FirstName),
		buttons: [][]models.InlineKeyboardButton{row(
			button(h.t.JoinButton, cbJoin+":"+code),
			button(h.t.DeclineButton, cbDecline),
		)},
	})
}

func (h *Handler) join(ctx context.Context, s Sender, chatID int64, messageID int, u user.User, code string) {
	head, err := h.family.Invitation(ctx, code)
	if err == nil {
		_, err = h.family.Join(ctx, code, u.TelegramID)
	}
	switch {
	case errors.Is(err, family.ErrInviteInvalid):
		h.reply(ctx, s, chatID, messageID, view{text: h.t.InviteInvalid})
	case errors.Is(err, family.ErrAlreadyMember):
		h.reply(ctx, s, chatID, messageID, view{text: h.t.AlreadyMember})
	case errors.Is(err, family.ErrHeadHasMembers):
		h.reply(ctx, s, chatID, messageID, view{text: h.t.HeadHasMembers})
	case err != nil:
		h.replyOrFail(ctx, s, chatID, messageID, view{}, err)
	default:
		h.log.InfoContext(ctx, "joined a family", "user_id", u.TelegramID, "head_id", head.TelegramID)
		h.reply(ctx, s, chatID, messageID, view{text: texts.Fill(h.t.Joined, "head", head.FirstName)})
		h.notify(ctx, s, head.TelegramID, texts.Fill(h.t.MemberJoined, "name", u.FirstName))
	}
}

func (h *Handler) showFamily(ctx context.Context, s Sender, chatID int64, u user.User) {
	v, err := h.familyView(ctx, u)
	h.replyOrFail(ctx, s, chatID, 0, v, err)
}

func (h *Handler) familyView(ctx context.Context, u user.User) (view, error) {
	members, err := h.family.Members(ctx, u.FamilyID)
	if err != nil {
		return view{}, err
	}
	invite := row(button(h.t.InviteButton, cbInvite))
	if len(members) <= 1 {
		return view{text: h.t.FamilyAlone, buttons: [][]models.InlineKeyboardButton{invite}}, nil
	}
	list := memberList(members, u.TelegramID)
	if !u.IsHead {
		return view{
			text:    texts.Fill(h.t.FamilyMember, "head", headName(members), "members", list),
			buttons: [][]models.InlineKeyboardButton{row(button(h.t.LeaveButton, cbLeave))},
		}, nil
	}
	buttons := [][]models.InlineKeyboardButton{invite}
	for _, m := range members {
		if !m.IsHead {
			buttons = append(buttons, row(button(
				texts.Fill(h.t.KickButton, "name", m.FirstName), cbKick+":"+strconv.FormatInt(m.TelegramID, 10))))
		}
	}
	return view{text: texts.Fill(h.t.FamilyHead, "members", list), buttons: buttons}, nil
}

func memberList(members []user.User, self int64) string {
	lines := make([]string, len(members))
	for i, m := range members {
		lines[i] = "• " + m.FirstName
		if m.IsHead {
			lines[i] += " — глава"
		}
		if m.TelegramID == self {
			lines[i] += " (вы)"
		}
	}
	return strings.Join(lines, "\n")
}

func headName(members []user.User) string {
	for _, m := range members {
		if m.IsHead {
			return m.FirstName
		}
	}
	return ""
}

func (h *Handler) createInvite(ctx context.Context, s Sender, chatID int64, messageID int, u user.User) {
	inv, err := h.family.CreateInvite(ctx, u)
	if errors.Is(err, family.ErrNotHead) {
		v, ferr := h.familyView(ctx, u) // the user lost the head role meanwhile
		h.replyOrFail(ctx, s, chatID, messageID, v, ferr)
		return
	}
	if err != nil {
		h.replyOrFail(ctx, s, chatID, messageID, view{}, err)
		return
	}
	days := strconv.Itoa(int(family.InviteTTL.Hours() / 24))
	// A new message, so the link stays in the chat while the family view is reused.
	h.reply(ctx, s, chatID, 0, view{text: texts.Fill(h.t.InviteCreated, "link", inv.Link, "days", days)})
}

func (h *Handler) confirmLeave(ctx context.Context, s Sender, chatID int64, messageID int, u user.User) {
	members, err := h.family.Members(ctx, u.FamilyID)
	if err != nil {
		h.replyOrFail(ctx, s, chatID, messageID, view{}, err)
		return
	}
	h.reply(ctx, s, chatID, messageID, view{
		text: texts.Fill(h.t.LeaveConfirm, "head", headName(members)),
		buttons: [][]models.InlineKeyboardButton{row(
			button(h.t.LeaveYes, cbLeaveYes),
			button(h.t.NoButton, cbFamily),
		)},
	})
}

func (h *Handler) leave(ctx context.Context, s Sender, chatID int64, messageID int, u user.User) {
	members, err := h.family.Members(ctx, u.FamilyID)
	if err == nil {
		_, err = h.family.Leave(ctx, u.TelegramID)
	}
	if errors.Is(err, family.ErrAlone) || errors.Is(err, family.ErrHeadHasMembers) {
		v, ferr := h.familyView(ctx, u)
		h.replyOrFail(ctx, s, chatID, messageID, v, ferr)
		return
	}
	if err != nil {
		h.replyOrFail(ctx, s, chatID, messageID, view{}, err)
		return
	}
	h.log.InfoContext(ctx, "left the family", "user_id", u.TelegramID)
	h.reply(ctx, s, chatID, messageID, view{text: h.t.Left})
	for _, m := range members {
		if m.IsHead {
			h.notify(ctx, s, m.TelegramID, texts.Fill(h.t.MemberLeft, "name", u.FirstName))
		}
	}
}

func (h *Handler) confirmKick(ctx context.Context, s Sender, chatID int64, messageID int, u user.User, id int64) {
	m, err := h.familyMember(ctx, u, id)
	if err != nil {
		v, ferr := h.familyView(ctx, u)
		h.replyOrFail(ctx, s, chatID, messageID, v, ferr)
		return
	}
	h.reply(ctx, s, chatID, messageID, view{
		text: texts.Fill(h.t.KickConfirm, "name", m.FirstName),
		buttons: [][]models.InlineKeyboardButton{row(
			button(h.t.KickYes, cbKickYes+":"+strconv.FormatInt(id, 10)),
			button(h.t.NoButton, cbFamily),
		)},
	})
}

func (h *Handler) kick(ctx context.Context, s Sender, chatID int64, messageID int, u user.User, id int64) {
	m, err := h.familyMember(ctx, u, id)
	if err == nil {
		err = h.family.Remove(ctx, u, id)
	}
	if errors.Is(err, family.ErrNotMember) || errors.Is(err, family.ErrNotHead) || errors.Is(err, family.ErrHeadRemoval) {
		v, ferr := h.familyView(ctx, u) // the family changed meanwhile: show it as it is
		h.replyOrFail(ctx, s, chatID, messageID, v, ferr)
		return
	}
	if err != nil {
		h.replyOrFail(ctx, s, chatID, messageID, view{}, err)
		return
	}
	h.log.InfoContext(ctx, "removed from the family", "user_id", id, "head_id", u.TelegramID)
	h.reply(ctx, s, chatID, messageID, view{text: texts.Fill(h.t.Kicked, "name", m.FirstName)})
	h.notify(ctx, s, id, h.t.YouWereRemoved)
}

// familyMember returns a member of u's family, which only its head may manage.
func (h *Handler) familyMember(ctx context.Context, u user.User, id int64) (user.User, error) {
	if !u.IsHead {
		return user.User{}, family.ErrNotHead
	}
	m, err := h.family.Get(ctx, id)
	if errors.Is(err, user.ErrNotFound) || err == nil && m.FamilyID != u.FamilyID {
		return user.User{}, family.ErrNotMember
	}
	return m, err
}

// notify tells another user about a change; they may have blocked the bot,
// which is not the caller's problem.
func (h *Handler) notify(ctx context.Context, s Sender, chatID int64, text string) {
	_ = h.Send(ctx, s, chatID, text)
}

// Settings.

func (h *Handler) settingsView(u user.User) view {
	weekly, weeklyButton := h.t.WeeklyOff, button(h.t.WeeklyButtonOff, cbWeekly+":on")
	if u.WeeklyReport {
		weekly, weeklyButton = h.t.WeeklyOn, button(h.t.WeeklyButtonOn, cbWeekly+":off")
	}
	toggle := func(on bool, name, onLabel, offLabel string) (string, models.InlineKeyboardButton) {
		if on {
			return h.t.FlagOn, button(onLabel, cbNotify+":"+name+":off")
		}
		return h.t.FlagOff, button(offLabel, cbNotify+":"+name+":on")
	}
	rates, ratesButton := toggle(u.ShowRates, notifyRates, h.t.RatesButtonOn, h.t.RatesButtonOff)
	goals, goalsButton := toggle(u.GoalUpdates, notifyGoals, h.t.GoalsButtonOn, h.t.GoalsButtonOff)
	streak, streakButton := toggle(u.StreakReminder, notifyStreak, h.t.StreakButtonOn, h.t.StreakButtonOff)
	morning := h.t.MorningOff
	buttons := [][]models.InlineKeyboardButton{row(weeklyButton)}
	if u.Reminders {
		morning = texts.Fill(h.t.MorningAt, "hour", hourLabel(u.ReminderHour))
		buttons = append(buttons,
			row(button(h.t.MorningButtonOn, cbNotify+":"+notifyMorning+":off")),
			row(button(h.t.MorningHour, cbHour)),
			row(ratesButton))
	} else {
		buttons = append(buttons, row(button(h.t.MorningButtonOff, cbNotify+":"+notifyMorning+":on")))
	}
	buttons = append(buttons, row(goalsButton), row(streakButton), row(button(h.t.TimezoneButton, cbTimezone)))
	return view{
		text: texts.Fill(h.t.Settings, "weekly", weekly, "morning", morning, "rates", rates, "goals", goals, "streak", streak,
			"timezone", timezoneName(u.Location(h.cfg.Location))),
		buttons: buttons,
	}
}

// Names of the switches in /settings.
const (
	notifyMorning = "morning"
	notifyRates   = "rates"
	notifyGoals   = "goals"
	notifyStreak  = "streak"
)

func (h *Handler) toggleNotification(ctx context.Context, s Sender, chatID int64, messageID int, u user.User, name string, on bool) {
	n := u.Notifications
	switch name {
	case notifyMorning:
		n.Reminders = on
	case notifyRates:
		n.ShowRates = on
	case notifyGoals:
		n.GoalUpdates = on
	case notifyStreak:
		n.StreakReminder = on
	default:
		return
	}
	updated, err := h.family.SetNotifications(ctx, u.TelegramID, n)
	h.replyOrFail(ctx, s, chatID, messageID, h.settingsView(updated), err)
}

func (h *Handler) setHour(ctx context.Context, s Sender, chatID int64, messageID int, u user.User, arg string) {
	hour, err := strconv.Atoi(arg)
	if err != nil {
		return
	}
	n := u.Notifications
	n.ReminderHour = hour
	updated, err := h.family.SetNotifications(ctx, u.TelegramID, n)
	h.replyOrFail(ctx, s, chatID, messageID, h.settingsView(updated), err)
}

// Hours of the picker: the morning and a little around it.
const (
	firstHour = 6
	lastHour  = 23
	hourRow   = 3
)

func (h *Handler) hourView() view {
	var buttons [][]models.InlineKeyboardButton
	for hour := firstHour; hour <= lastHour; hour++ {
		b := button(hourLabel(hour), cbHour+":"+strconv.Itoa(hour))
		if (hour-firstHour)%hourRow == 0 {
			buttons = append(buttons, row(b))
		} else {
			buttons[len(buttons)-1] = append(buttons[len(buttons)-1], b)
		}
	}
	buttons = append(buttons, row(button(h.t.BackButton, cbSettings)))
	return view{text: h.t.HourPick, buttons: buttons}
}

func hourLabel(hour int) string { return strconv.Itoa(hour) + ":00" }

func (h *Handler) timezoneView() view {
	var buttons [][]models.InlineKeyboardButton
	for i := 0; i < len(timezones); i += 2 {
		r := row(button(timezones[i].label(), cbTimezone+":"+timezones[i].name))
		if i+1 < len(timezones) {
			r = append(r, button(timezones[i+1].label(), cbTimezone+":"+timezones[i+1].name))
		}
		buttons = append(buttons, r)
	}
	buttons = append(buttons, row(button(h.t.BackButton, cbSettings)))
	return view{text: h.t.TimezonePick, buttons: buttons}
}

// Account.

func (h *Handler) confirmDelete(ctx context.Context, s Sender, chatID int64, u user.User) {
	text := h.t.DeleteConfirm
	if u.IsHead {
		members, err := h.family.Members(ctx, u.FamilyID)
		if err != nil {
			h.replyOrFail(ctx, s, chatID, 0, view{}, err)
			return
		}
		if len(members) > 1 {
			text = h.t.DeleteConfirmHead
		}
	}
	h.reply(ctx, s, chatID, 0, view{
		text: text,
		buttons: [][]models.InlineKeyboardButton{row(
			button(h.t.DeleteYes, cbDelete),
			button(h.t.NoButton, cbCancel),
		)},
	})
}

func (h *Handler) deleteAccount(ctx context.Context, s Sender, chatID int64, messageID int, u user.User) {
	if err := h.family.DeleteAccount(ctx, u.TelegramID); err != nil {
		h.replyOrFail(ctx, s, chatID, messageID, view{}, err)
		return
	}
	h.agent.Reset(ctx, u.TelegramID)
	h.log.InfoContext(ctx, "account deleted", "user_id", u.TelegramID)
	h.reply(ctx, s, chatID, messageID, view{text: h.t.Deleted})
}

func (h *Handler) feedback(ctx context.Context, s Sender, chatID int64, u user.User, text string) {
	if text == "" {
		h.send(ctx, s, chatID, h.t.FeedbackUsage)
		return
	}
	h.log.InfoContext(ctx, "feedback", "user_id", u.TelegramID, "text", text)
	if h.cfg.AdminID != 0 {
		username := "без ника"
		if u.Username != "" {
			username = "@" + u.Username
		}
		h.notify(ctx, s, h.cfg.AdminID, texts.Fill(h.t.FeedbackToAdmin,
			"name", u.FirstName, "username", username, "id", strconv.FormatInt(u.TelegramID, 10), "text", text))
	}
	h.send(ctx, s, chatID, h.t.FeedbackThanks)
}

// appView is a button that opens the mini app, or a note that it is coming.
func (h *Handler) appView() view {
	if h.cfg.MiniAppURL == "" {
		return view{text: h.t.AppMissing}
	}
	return view{
		text: h.t.AppOpen,
		buttons: [][]models.InlineKeyboardButton{{
			{Text: h.t.AppButton, WebApp: &models.WebAppInfo{URL: h.cfg.MiniAppURL}},
		}},
	}
}

// MenuButton opens the mini app from the button left of the input field;
// nil when there is no mini app to open.
func MenuButton(t texts.Bot, miniAppURL string) models.InputMenuButton {
	if miniAppURL == "" {
		return nil
	}
	return models.MenuButtonWebApp{
		Type:   models.MenuButtonTypeWebApp,
		Text:   t.AppButton,
		WebApp: models.WebAppInfo{URL: miniAppURL},
	}
}

// Commands returns the command menu shown by Telegram next to the input field.
func Commands(t texts.Bot, withApp bool) []models.BotCommand {
	commands := []models.BotCommand{{Command: "start", Description: t.CommandStart}}
	if withApp {
		commands = append(commands, models.BotCommand{Command: "app", Description: t.CommandApp})
	}
	commands = append(commands,
		models.BotCommand{Command: "dream", Description: t.CommandDream},
		models.BotCommand{Command: "quests", Description: t.CommandQuests},
		models.BotCommand{Command: "level", Description: t.CommandLevel},
		models.BotCommand{Command: "challenge", Description: t.CommandChallenge},
		models.BotCommand{Command: "review", Description: t.CommandReview})
	return append(commands, []models.BotCommand{
		{Command: "family", Description: t.CommandFamily},
		{Command: "settings", Description: t.CommandSettings},
		{Command: "privacy", Description: t.CommandPrivacy},
		{Command: "feedback", Description: t.CommandFeedback},
		{Command: "delete_me", Description: t.CommandDeleteMe},
		{Command: "help", Description: t.CommandHelp},
	}...)
}
