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

// The code counts on these lists: buttons go by position.
func TestListsHaveTheirLength(t *testing.T) {
	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][2]int{
		"dream_terms":      {len(b.DreamTerms), 4},
		"level_names":      {len(b.LevelNames), 6},
		"level_conditions": {len(b.LevelConditions), 5},
		"level_questions":  {len(b.LevelQuestions), 5},
	} {
		if got[0] != got[1] {
			t.Errorf("%s has %d items, want %d", name, got[0], got[1])
		}
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
