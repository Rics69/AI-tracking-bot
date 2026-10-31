package startparam

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		raw  string
		want Param
	}{
		{"", Param{Kind: Plain, Source: SourceDirect}},
		{"hello world", Param{Kind: Plain, Source: SourceDirect}},
		{"привет", Param{Kind: Plain, Source: SourceDirect}},
		{strings.Repeat("a", 65), Param{Kind: Plain, Source: SourceDirect}},
		{"AbC-123_xyzAbC-123_xyz", Param{Kind: Invite, Raw: "AbC-123_xyzAbC-123_xyz", Source: SourceInvite}},
		{"go_dream__reels", Param{Kind: Feature, Raw: "go_dream__reels", Feature: "dream", Source: "reels"}},
		{"go_goal__tg", Param{Kind: Feature, Raw: "go_goal__tg", Feature: "goal", Source: "tg"}},
		{"go_limit__yt", Param{Kind: Feature, Raw: "go_limit__yt", Feature: "limit", Source: "yt"}},
		{"go_review", Param{Kind: Feature, Raw: "go_review", Feature: "review", Source: "go_review"}},
		{"go_abcdefghijklmnopqrs", Param{Kind: Unknown, Raw: "go_abcdefghijklmnopqrs", Source: "go_abcdefghijklmnopqrs"}},
		{"ch_noshopping", Param{Kind: Challenge, Raw: "ch_noshopping", Slug: "noshopping", Source: "ch_noshopping"}},
		{"ref_a1B2", Param{Kind: Referral, Raw: "ref_a1B2", Ref: "a1B2", Source: SourceRef}},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := Parse(tt.raw); got != tt.want {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestLabelledMayStillBeAnInvite(t *testing.T) {
	for raw, want := range map[string]bool{
		"":              false,
		"plainInvite":   false,
		"go_dream__x":   true,
		"go_unknown":    true,
		"ch_x":          true,
		"ref_x":         true,
		"not valid !!!": false,
	} {
		if got := Parse(raw).Labelled(); got != want {
			t.Errorf("Parse(%q).Labelled() = %v, want %v", raw, got, want)
		}
	}
	if p := Parse("ch_x").AsInvite(); p.Kind != Invite || p.Raw != "ch_x" || p.Source != SourceInvite {
		t.Errorf("AsInvite = %+v", p)
	}
}
