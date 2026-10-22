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

	Settings        string `yaml:"settings"`
	WeeklyOn        string `yaml:"weekly_on"`
	WeeklyOff       string `yaml:"weekly_off"`
	WeeklyButtonOn  string `yaml:"weekly_button_on"`
	WeeklyButtonOff string `yaml:"weekly_button_off"`
	TimezoneButton  string `yaml:"timezone_button"`
	TimezonePick    string `yaml:"timezone_pick"`
	BackButton      string `yaml:"back_button"`

	Privacy     string `yaml:"privacy"`
	PrivacyLink string `yaml:"privacy_link"`

	DeleteConfirm     string `yaml:"delete_confirm"`
	DeleteConfirmHead string `yaml:"delete_confirm_head"`
	DeleteYes         string `yaml:"delete_yes"`
	Deleted           string `yaml:"deleted"`

	AppButton  string `yaml:"app_button"`
	AppOpen    string `yaml:"app_open"`
	AppMissing string `yaml:"app_missing"`

	FeedbackUsage   string `yaml:"feedback_usage"`
	FeedbackThanks  string `yaml:"feedback_thanks"`
	FeedbackToAdmin string `yaml:"feedback_to_admin"`

	CommandStart    string `yaml:"command_start"`
	CommandApp      string `yaml:"command_app"`
	CommandFamily   string `yaml:"command_family"`
	CommandSettings string `yaml:"command_settings"`
	CommandPrivacy  string `yaml:"command_privacy"`
	CommandFeedback string `yaml:"command_feedback"`
	CommandDeleteMe string `yaml:"command_delete_me"`
	CommandHelp     string `yaml:"command_help"`
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
	v, t := reflect.ValueOf(b), reflect.TypeOf(b)
	for i := range t.NumField() {
		if strings.TrimSpace(v.Field(i).String()) == "" {
			return Bot{}, fmt.Errorf("texts.yaml: %q is missing or empty", t.Field(i).Tag.Get("yaml"))
		}
	}
	return b, nil
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
