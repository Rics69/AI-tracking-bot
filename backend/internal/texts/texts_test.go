package texts

import (
	"strings"
	"testing"
)

func TestEmbeddedTextsAreComplete(t *testing.T) {
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}

func TestMissingKeyIsAnError(t *testing.T) {
	if _, err := parse([]byte("consent: Привет\n")); err == nil || !strings.Contains(err.Error(), "consent_button") {
		t.Errorf("err = %v, want a missing consent_button", err)
	}
	if _, err := parse(append(raw, []byte("\nunknown_key: x\n")...)); err == nil {
		t.Error("an unknown key must be an error: it is probably a typo")
	}
}

func TestFill(t *testing.T) {
	if got := Fill("{head} приглашает, {head}!", "head", "Анна"); got != "Анна приглашает, Анна!" {
		t.Errorf("got %q", got)
	}
}
