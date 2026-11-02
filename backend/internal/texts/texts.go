// Package texts holds what the bot says, in texts.yaml: the wording is meant
// to be edited without touching code. The file is embedded at build time and
// checked at startup: every key must be present and non-empty.
package texts

import (
	"bytes"
	_ "embed"
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed texts.yaml
var raw []byte

// Bot is every message of the bot. Fields map to keys of texts.yaml.
type Bot struct {
	Consent       string `yaml:"consent"`
	ConsentButton string `yaml:"consent_button"`
	Welcome       string `yaml:"welcome"`
	Help          string `yaml:"help"`
	NotText       string `yaml:"not_text"`
	TooLong       string `yaml:"too_long"`
	Private       string `yaml:"private"`
	Failure       string `yaml:"failure"`
	Blocked       string `yaml:"blocked"`
	LimitReached  string `yaml:"limit_reached"`
	Cancelled     string `yaml:"cancelled"`

	QuickSaved        string `yaml:"quick_saved"`
	QuickSavedIncome  string `yaml:"quick_saved_income"`
	QuickFixButton    string `yaml:"quick_fix_button"`
	QuickFixHint      string `yaml:"quick_fix_hint"`
	QuickDeleteButton string `yaml:"quick_delete_button"`
	QuickDeleted      string `yaml:"quick_deleted"`
	QuickGone         string `yaml:"quick_gone"`
	AgentResting      string `yaml:"agent_resting"`

	SkipButton string `yaml:"skip_button"`
	ExitButton string `yaml:"exit_button"`
	FlowLeft   string `yaml:"flow_left"`
	FlowStale  string `yaml:"flow_stale"`

	FlowDone      string `yaml:"flow_done"`
	FlowDoneLimit string `yaml:"flow_done_limit"`

	OnboardChoice     string `yaml:"onboard_choice"`
	OnboardDream      string `yaml:"onboard_dream"`
	OnboardGoal       string `yaml:"onboard_goal"`
	OnboardJust       string `yaml:"onboard_just"`
	OnboardGoalTitle  string `yaml:"onboard_goal_title"`
	OnboardGoalAmount string `yaml:"onboard_goal_amount"`
	OnboardGoalSaved  string `yaml:"onboard_goal_saved"`
	OnboardPayday     string `yaml:"onboard_payday"`
	OnboardIncome     string `yaml:"onboard_income"`
	OnboardMorning    string `yaml:"onboard_morning"`
	OnboardNoMorning  string `yaml:"onboard_no_morning"`
	OnboardRecord     string `yaml:"onboard_record"`

	RetryTitle  string `yaml:"retry_title"`
	RetryAmount string `yaml:"retry_amount"`
	RetryPayday string `yaml:"retry_payday"`
	RetryDate   string `yaml:"retry_date"`
	RetryButton string `yaml:"retry_button"`

	DreamTitle       string   `yaml:"dream_title"`
	DreamPresets     []string `yaml:"dream_presets"`
	DreamPrice       string   `yaml:"dream_price"`
	DreamTerm        string   `yaml:"dream_term"`
	DreamTerms       []string `yaml:"dream_terms"`
	DreamCustomDate  string   `yaml:"dream_custom_date"`
	DreamDate        string   `yaml:"dream_date"`
	DreamSaved       string   `yaml:"dream_saved"`
	DreamZero        string   `yaml:"dream_zero"`
	DreamResult      string   `yaml:"dream_result"`
	DreamShare       string   `yaml:"dream_share"`
	DreamRealistic   string   `yaml:"dream_realistic"`
	DreamAmbitious   string   `yaml:"dream_ambitious"`
	DreamStretch     string   `yaml:"dream_stretch"`
	DreamAllThere    string   `yaml:"dream_all_there"`
	DreamBoostNote   string   `yaml:"dream_boost_note"`
	DreamMakeGoal    string   `yaml:"dream_make_goal"`
	DreamBoostButton string   `yaml:"dream_boost_button"`
	DreamLater       string   `yaml:"dream_later"`
	DreamGoalSaved   string   `yaml:"dream_goal_saved"`

	DebtsNone string `yaml:"debts_none"`
	DebtsView string `yaml:"debts_view"`
	DebtsLine string `yaml:"debts_line"`

	QuestsHeader    string `yaml:"quests_header"`
	QuestLine       string `yaml:"quest_line"`
	QuestClosedLine string `yaml:"quest_closed_line"`
	QuestListButton string `yaml:"quest_list_button"`
	QuestDoneButton string `yaml:"quest_done_button"`
	QuestDone       string `yaml:"quest_done"`
	QuestAlready    string `yaml:"quest_already"`
	NoSpendButton   string `yaml:"no_spend_button"`
	NoSpendDone     string `yaml:"no_spend_done"`
	StreakNudge     string `yaml:"streak_nudge"`
	StreakMark      string `yaml:"streak_mark"`
	MorningQuest    string `yaml:"morning_quest"`

	MorningTasks   string `yaml:"morning_tasks"`
	TaskReminder   string `yaml:"task_reminder"`
	TaskDoneButton string `yaml:"task_done_button"`
	TaskDone       string `yaml:"task_done"`
	TaskDoneGoal   string `yaml:"task_done_goal"`

	LevelNames         []string `yaml:"level_names"`
	LevelConditions    []string `yaml:"level_conditions"`
	LevelQuestions     []string `yaml:"level_questions"`
	LevelTop           string   `yaml:"level_top"`
	LevelYes           string   `yaml:"level_yes"`
	LevelNo            string   `yaml:"level_no"`
	LevelResult        string   `yaml:"level_result"`
	LevelView          string   `yaml:"level_view"`
	LevelUp            string   `yaml:"level_up"`
	LevelConfirmButton string   `yaml:"level_confirm_button"`

	MorningChallenge       string `yaml:"morning_challenge"`
	MorningShare           string `yaml:"morning_share"`
	ChallengeDoneButton    string `yaml:"challenge_done_button"`
	ChallengeLaterButton   string `yaml:"challenge_later_button"`
	ChallengeSkipButton    string `yaml:"challenge_skip_button"`
	ChallengeLaterOK       string `yaml:"challenge_later_ok"`
	ChallengeSkipped       string `yaml:"challenge_skipped"`
	ChallengeEvening       string `yaml:"challenge_evening"`
	ChallengeItem          string `yaml:"challenge_item"`
	ChallengeNone          string `yaml:"challenge_none"`
	ChallengeNotStarted    string `yaml:"challenge_not_started"`
	ChallengeCurrent       string `yaml:"challenge_current"`
	ChallengeJoinButton    string `yaml:"challenge_join_button"`
	ChallengeLeaveButton   string `yaml:"challenge_leave_button"`
	ChallengeJoined        string `yaml:"challenge_joined"`
	ChallengeAlready       string `yaml:"challenge_already"`
	ChallengeClosed        string `yaml:"challenge_closed"`
	ChallengeClubOnly      string `yaml:"challenge_club_only"`
	ChallengeLeft          string `yaml:"challenge_left"`
	ChallengeFinished      string `yaml:"challenge_finished"`
	ChallengeFinishedLimit string `yaml:"challenge_finished_limit"`

	ReferralLine     string `yaml:"referral_line"`
	ReferralRewarded string `yaml:"referral_rewarded"`
	ReferralClub     string `yaml:"referral_club"`

	ClubAbout      string `yaml:"club_about"`
	ClubActive     string `yaml:"club_active"`
	ClubChatButton string `yaml:"club_chat_button"`
	ClubLinks      string `yaml:"club_links"`
	ClubGranted    string `yaml:"club_granted"`
	ClubEnding     string `yaml:"club_ending"`
	PromoUsage     string `yaml:"promo_usage"`
	PromoInvalid   string `yaml:"promo_invalid"`
	PromoUsed      string `yaml:"promo_used"`
	PromoDone      string `yaml:"promo_done"`

	AdminGrantUsage  string `yaml:"admin_grant_usage"`
	AdminGranted     string `yaml:"admin_granted"`
	AdminRevokeUsage string `yaml:"admin_revoke_usage"`
	AdminRevoked     string `yaml:"admin_revoked"`
	AdminPromoUsage  string `yaml:"admin_promo_usage"`
	AdminPromo       string `yaml:"admin_promo"`
	AdminLimitsUsage string `yaml:"admin_limits_usage"`
	AdminLimits      string `yaml:"admin_limits"`

	CardGoal            string `yaml:"card_goal"`
	CardAmounts         string `yaml:"card_amounts"`
	CardLevel           string `yaml:"card_level"`
	CardLevelValue      string `yaml:"card_level_value"`
	CardStreak          string `yaml:"card_streak"`
	CardChallenge       string `yaml:"card_challenge"`
	CardChallengeDays   string `yaml:"card_challenge_days"`
	CardDream           string `yaml:"card_dream"`
	CardDreamMonths     string `yaml:"card_dream_months"`
	CardDreamDeadline   string `yaml:"card_dream_deadline"`
	CardDreamShare      string `yaml:"card_dream_share"`
	CardDreamAmounts    string `yaml:"card_dream_amounts"`
	CardButton          string `yaml:"card_button"`
	CardShowAmounts     string `yaml:"card_show_amounts"`
	GoalProgressCaption string `yaml:"goal_progress_caption"`
	GoalReachedCaption  string `yaml:"goal_reached_caption"`

	WeekGoal         string `yaml:"week_goal"`
	WeekLimit        string `yaml:"week_limit"`
	WeekStreak       string `yaml:"week_streak"`
	WeekQuests       string `yaml:"week_quests"`
	WeekGoalClose    string `yaml:"week_goal_close"`
	WeekStreakBroken string `yaml:"week_streak_broken"`
	WeekSpentLess    string `yaml:"week_spent_less"`
	WeekSpentMore    string `yaml:"week_spent_more"`

	ReviewIntro         string   `yaml:"review_intro"`
	ReviewFromData      string   `yaml:"review_from_data"`
	ReviewManual        string   `yaml:"review_manual"`
	ReviewAge           string   `yaml:"review_age"`
	ReviewAges          []string `yaml:"review_ages"`
	ReviewIncome        string   `yaml:"review_income"`
	ReviewExpenses      string   `yaml:"review_expenses"`
	ReviewDebts         string   `yaml:"review_debts"`
	ReviewNoDebts       string   `yaml:"review_no_debts"`
	ReviewRate          string   `yaml:"review_rate"`
	ReviewSavings       string   `yaml:"review_savings"`
	ReviewGoal          string   `yaml:"review_goal"`
	ReviewQuestion      string   `yaml:"review_question"`
	ReviewConsent       string   `yaml:"review_consent"`
	ReviewConsentButton string   `yaml:"review_consent_button"`
	ReviewThanks        string   `yaml:"review_thanks"`
	RetryRate           string   `yaml:"retry_rate"`
	RetryLong           string   `yaml:"retry_long"`

	Invitation     string `yaml:"invitation"`
	JoinButton     string `yaml:"join_button"`
	DeclineButton  string `yaml:"decline_button"`
	Joined         string `yaml:"joined"`
	MemberJoined   string `yaml:"member_joined"`
	Declined       string `yaml:"declined"`
	InviteInvalid  string `yaml:"invite_invalid"`
	AlreadyMember  string `yaml:"already_member"`
	HeadHasMembers string `yaml:"head_has_members"`

	FamilyAlone    string `yaml:"family_alone"`
	FamilyHead     string `yaml:"family_head"`
	FamilyMember   string `yaml:"family_member"`
	InviteButton   string `yaml:"invite_button"`
	InviteCreated  string `yaml:"invite_created"`
	LeaveButton    string `yaml:"leave_button"`
	LeaveConfirm   string `yaml:"leave_confirm"`
	LeaveYes       string `yaml:"leave_yes"`
	Left           string `yaml:"left"`
	MemberLeft     string `yaml:"member_left"`
	KickButton     string `yaml:"kick_button"`
	KickConfirm    string `yaml:"kick_confirm"`
	KickYes        string `yaml:"kick_yes"`
	Kicked         string `yaml:"kicked"`
	YouWereRemoved string `yaml:"you_were_removed"`
	NoButton       string `yaml:"no_button"`

	Settings         string `yaml:"settings"`
	WeeklyOn         string `yaml:"weekly_on"`
	WeeklyOff        string `yaml:"weekly_off"`
	WeeklyButtonOn   string `yaml:"weekly_button_on"`
	WeeklyButtonOff  string `yaml:"weekly_button_off"`
	MorningAt        string `yaml:"morning_at"`
	MorningOff       string `yaml:"morning_off"`
	MorningButtonOn  string `yaml:"morning_button_on"`
	MorningButtonOff string `yaml:"morning_button_off"`
	MorningHour      string `yaml:"morning_hour"`
	HourPick         string `yaml:"hour_pick"`
	RatesButtonOn    string `yaml:"rates_button_on"`
	RatesButtonOff   string `yaml:"rates_button_off"`
	GoalsButtonOn    string `yaml:"goals_button_on"`
	GoalsButtonOff   string `yaml:"goals_button_off"`
	StreakButtonOn   string `yaml:"streak_button_on"`
	StreakButtonOff  string `yaml:"streak_button_off"`
	FlagOn           string `yaml:"flag_on"`
	FlagOff          string `yaml:"flag_off"`
	TimezoneButton   string `yaml:"timezone_button"`
	TimezonePick     string `yaml:"timezone_pick"`
	BackButton       string `yaml:"back_button"`

	Privacy     string `yaml:"privacy"`
	PrivacyLink string `yaml:"privacy_link"`

	DeleteConfirm     string `yaml:"delete_confirm"`
	DeleteConfirmHead string `yaml:"delete_confirm_head"`
	DeleteYes         string `yaml:"delete_yes"`
	Deleted           string `yaml:"deleted"`

	AppButton  string `yaml:"app_button"`
	AppOpen    string `yaml:"app_open"`
	AppMissing string `yaml:"app_missing"`

	Reminders        Reminders `yaml:"reminders"`
	ReminderNoIncome string    `yaml:"reminder_no_income"`
	ReminderPayment  string    `yaml:"reminder_payment"`
	ReminderGoal     string    `yaml:"reminder_goal"`
	ReminderDebt     string    `yaml:"reminder_debt"`
	DebtFirstClosed  string    `yaml:"debt_first_closed"`
	ReminderRates    string    `yaml:"reminder_rates"`
	Comeback         string    `yaml:"comeback"`
	ComebackGoal     string    `yaml:"comeback_goal"`
	GoalProgress     string    `yaml:"goal_progress"`
	GoalReached      string    `yaml:"goal_reached"`

	AdminBanUsage         string `yaml:"admin_ban_usage"`
	AdminBanned           string `yaml:"admin_banned"`
	AdminUnbanned         string `yaml:"admin_unbanned"`
	AdminUnknownUser      string `yaml:"admin_unknown_user"`
	AdminSelf             string `yaml:"admin_self"`
	LLMAlert              string `yaml:"llm_alert"`
	AdminChallenge        string `yaml:"admin_challenge"`
	AdminChallengeUsage   string `yaml:"admin_challenge_usage"`
	AdminChallengeUnknown string `yaml:"admin_challenge_unknown"`
	AdminReviews          string `yaml:"admin_reviews"`
	AdminNoReviews        string `yaml:"admin_no_reviews"`
	AdminReviewNew        string `yaml:"admin_review_new"`
	PackageAlert          string `yaml:"package_alert"`
	PackageOut            string `yaml:"package_out"`

	FeedbackUsage   string `yaml:"feedback_usage"`
	FeedbackThanks  string `yaml:"feedback_thanks"`
	FeedbackToAdmin string `yaml:"feedback_to_admin"`

	CommandStart     string `yaml:"command_start"`
	CommandApp       string `yaml:"command_app"`
	CommandDream     string `yaml:"command_dream"`
	CommandQuests    string `yaml:"command_quests"`
	CommandLevel     string `yaml:"command_level"`
	CommandChallenge string `yaml:"command_challenge"`
	CommandReview    string `yaml:"command_review"`
	CommandReviews   string `yaml:"command_reviews"`
	CommandClub      string `yaml:"command_club"`
	CommandDebts     string `yaml:"command_debts"`
	CommandGrant     string `yaml:"command_grant"`
	CommandRevoke    string `yaml:"command_revoke"`
	CommandNewPromo  string `yaml:"command_new_promo"`
	CommandLimits    string `yaml:"command_limits"`
	CommandFamily    string `yaml:"command_family"`
	CommandSettings  string `yaml:"command_settings"`
	CommandPrivacy   string `yaml:"command_privacy"`
	CommandFeedback  string `yaml:"command_feedback"`
	CommandDeleteMe  string `yaml:"command_delete_me"`
	CommandHelp      string `yaml:"command_help"`
	CommandStats     string `yaml:"command_stats"`
	CommandBan       string `yaml:"command_ban"`
	CommandUnban     string `yaml:"command_unban"`
}

// Reminders are the phrases of the morning message by the state of the money;
// one is picked at random.
type Reminders struct {
	Neutral []string `yaml:"neutral"`
	Full    []string `yaml:"full"`
	Empty   []string `yaml:"empty"`
	Hungry  []string `yaml:"hungry"`
	Fed     []string `yaml:"fed"`
}

// Load parses texts.yaml; unknown or missing keys are errors.
func Load() (Bot, error) {
	return parse(raw)
}

func parse(data []byte) (Bot, error) {
	var b Bot
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&b); err != nil {
		return Bot{}, fmt.Errorf("texts.yaml: %w", err)
	}
	if err := complete(reflect.ValueOf(b), ""); err != nil {
		return Bot{}, err
	}
	return b, nil
}

// complete checks that every string and every list of phrases is non-empty.
func complete(v reflect.Value, prefix string) error {
	t := v.Type()
	for i := range t.NumField() {
		key := prefix + t.Field(i).Tag.Get("yaml")
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Struct:
			if err := complete(f, key+"."); err != nil {
				return err
			}
		case reflect.Slice:
			if f.Len() == 0 {
				return fmt.Errorf("texts.yaml: %q is missing or empty", key)
			}
			for j := range f.Len() {
				if strings.TrimSpace(f.Index(j).String()) == "" {
					return fmt.Errorf("texts.yaml: %q has an empty phrase", key)
				}
			}
		default:
			if strings.TrimSpace(f.String()) == "" {
				return fmt.Errorf("texts.yaml: %q is missing or empty", key)
			}
		}
	}
	return nil
}

// Fill replaces {key} placeholders: Fill(t.Joined, "head", "Анна").
func Fill(text string, keyValues ...string) string {
	pairs := make([]string, len(keyValues))
	for i, s := range keyValues {
		if i%2 == 0 {
			s = "{" + s + "}"
		}
		pairs[i] = s
	}
	return strings.NewReplacer(pairs...).Replace(text)
}
