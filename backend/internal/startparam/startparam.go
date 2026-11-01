// Package startparam reads the parameter of a t.me/<bot>?start=<param> link:
// where the user came from and what they came for. It only parses: whether a
// parameter is a live invite is known to the family service.
package startparam

import (
	"regexp"
	"slices"
	"strings"
)

// Kind tells what a link leads to.
type Kind int

const (
	Plain     Kind = iota // no parameter, or one Telegram would not send
	Invite                // an invite code of a family
	Feature               // go_<feature>[__<source>]
	Challenge             // ch_<slug>
	Referral              // ref_<code>
	Unknown               // a known prefix with nothing we can do about it
)

// Sources of those who came without a label.
const (
	SourceDirect = "direct"
	SourceInvite = "invite"
	SourceRef    = "ref"
)

const (
	prefixFeature   = "go_"
	prefixChallenge = "ch_"
	prefixReferral  = "ref_"
)

// Features: the destinations of go_ links.
const (
	FeatureDream  = "dream"
	FeatureGoal   = "goal"
	FeatureLimit  = "limit"
	FeatureReview = "review"
)

var features = []string{FeatureDream, FeatureGoal, FeatureLimit, FeatureReview}

// valid is what Telegram accepts in a start parameter.
var valid = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Param is a parsed start parameter.
type Param struct {
	Kind    Kind
	Raw     string // as it came; empty for Plain
	Feature string // dream, goal, limit, review
	Slug    string // of a challenge
	Ref     string // referral code
	Source  string // what goes to first_source and last_source
}

// Labelled reports a parameter with one of the prefixes. Invite codes are
// random base64url and may look like that, so the caller still checks it as
// an invite: only a code the family service does not know is a label.
func (p Param) Labelled() bool {
	return p.Kind == Feature || p.Kind == Challenge || p.Kind == Referral || p.Kind == Unknown
}

// Parse never fails: anything malformed is the same as no parameter at all.
func Parse(raw string) Param {
	if !valid.MatchString(raw) {
		return Param{Kind: Plain, Source: SourceDirect}
	}
	p := Param{Raw: raw}
	switch {
	case strings.HasPrefix(raw, prefixFeature):
		name, source, _ := strings.Cut(strings.TrimPrefix(raw, prefixFeature), "__")
		if !slices.Contains(features, name) {
			p.Kind, p.Source = Unknown, raw
			return p
		}
		p.Kind, p.Feature, p.Source = Feature, name, source
		if source == "" {
			p.Source = raw // go_dream: a link without a campaign is a source of its own
		}
	case strings.HasPrefix(raw, prefixChallenge):
		p.Kind, p.Slug, p.Source = Challenge, strings.TrimPrefix(raw, prefixChallenge), raw
	case strings.HasPrefix(raw, prefixReferral):
		p.Kind, p.Ref, p.Source = Referral, strings.TrimPrefix(raw, prefixReferral), SourceRef
	default:
		p.Kind, p.Source = Invite, SourceInvite
	}
	return p
}

// AsInvite is p once the family service has recognised it as an invite code.
func (p Param) AsInvite() Param {
	return Param{Kind: Invite, Raw: p.Raw, Source: SourceInvite}
}
