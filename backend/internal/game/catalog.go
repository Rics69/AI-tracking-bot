package game

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

//go:embed quests.yaml
var rawQuests []byte

//go:embed challenges/*.yaml
var rawChallenges embed.FS

// Kinds and checks of quests.
const (
	KindDaily  = "daily"
	KindWeekly = "weekly"
	KindOnce   = "once"

	CheckManual = "manual"
	CheckAuto   = "auto"
)

// Rule types of automatic checks.
const (
	RuleOperationsLogged = "operations_logged"
	RuleUnderDailyLimit  = "under_daily_limit"
	RuleGoalContribution = "goal_contribution"
	RuleNoSpend          = "no_spend"
)

// Access of challenges.
const (
	AccessFree = "free"
	AccessClub = "club"
)

// Slugs go inside the data of a button, which holds 64 bytes.
var validSlug = regexp.MustCompile(`^[a-z0-9-]{1,31}$`)

// Rule is what an automatic check looks at.
type Rule struct {
	Type     string          `yaml:"type"`
	Count    int             `yaml:"count"`
	Category ledger.Category `yaml:"category"`
	Text     string          `yaml:"text"`
}

// AfterDay reports rules that can only be judged once the day is over.
func (r Rule) AfterDay() bool { return r.Type == RuleUnderDailyLimit || r.Type == RuleNoSpend }

func (r Rule) check() error {
	switch r.Type {
	case RuleUnderDailyLimit, RuleGoalContribution:
		return nil
	case RuleOperationsLogged:
		if r.Count < 1 {
			return fmt.Errorf("rule %s needs a count", r.Type)
		}
		return nil
	case RuleNoSpend:
		if r.Category != "" && (!r.Category.Valid() || r.Category.Kind() != ledger.KindExpense) {
			return fmt.Errorf("rule %s: %q is not an expense category", r.Type, r.Category)
		}
		return nil
	default:
		return fmt.Errorf("unknown rule type %q", r.Type)
	}
}

// Quest is a task from the catalog.
type Quest struct {
	Slug   string `yaml:"slug"`
	Title  string `yaml:"title"`
	Text   string `yaml:"text"`
	Levels [2]int `yaml:"levels"`
	Kind   string `yaml:"kind"`
	Check  string `yaml:"check"`
	Rule   Rule   `yaml:"rule"`
	XP     int    `yaml:"xp"`
}

// For reports whether the quest suits a level.
func (q Quest) For(level int) bool { return level >= q.Levels[0] && level <= q.Levels[1] }

// Period is the key of the quest's period that contains day.
func (q Quest) Period(day time.Time) string {
	switch q.Kind {
	case KindOnce:
		return periodOnce
	case KindWeekly:
		return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7)).Format(time.DateOnly)
	default:
		return day.Format(time.DateOnly)
	}
}

const periodOnce = "once"

func (q Quest) check() error {
	if !validSlug.MatchString(q.Slug) {
		return fmt.Errorf("quest slug %q: lowercase letters, digits and dashes, at most 31", q.Slug)
	}
	if q.Title == "" || q.Text == "" || q.XP <= 0 {
		return fmt.Errorf("quest %s: title, text and xp are required", q.Slug)
	}
	if q.Levels[0] < 0 || q.Levels[1] > MaxLevel || q.Levels[0] > q.Levels[1] {
		return fmt.Errorf("quest %s: bad levels %v", q.Slug, q.Levels)
	}
	if q.Kind != KindDaily && q.Kind != KindWeekly && q.Kind != KindOnce {
		return fmt.Errorf("quest %s: unknown kind %q", q.Slug, q.Kind)
	}
	switch q.Check {
	case CheckManual:
		return nil
	case CheckAuto:
		if q.Kind != KindDaily {
			return fmt.Errorf("quest %s: only daily quests are checked automatically", q.Slug)
		}
		return q.Rule.check()
	default:
		return fmt.Errorf("quest %s: unknown check %q", q.Slug, q.Check)
	}
}

// ChallengeDay is one day of a challenge.
type ChallengeDay struct {
	Title string `yaml:"title"`
	Task  string `yaml:"task"`
	Check string `yaml:"check"`
	Rule  Rule   `yaml:"rule"`
	XP    int    `yaml:"xp"`
}

// Challenge is a course of daily tasks.
type Challenge struct {
	Slug        string         `yaml:"slug"`
	Title       string         `yaml:"title"`
	Description string         `yaml:"description"`
	Access      string         `yaml:"access"`
	StartsOn    string         `yaml:"starts_on"` // YYYY-MM-DD; empty: on joining
	ChatURL     string         `yaml:"chat_url"`
	XPFinish    int            `yaml:"xp_finish"`
	Days        []ChallengeDay `yaml:"days"`

	start *time.Time // StartsOn, parsed
}

// closeAfterDays: a common stream takes new people for this long after the start.
const closeAfterDays = 3

// Open reports whether someone can join on day.
func (c Challenge) Open(day time.Time) bool {
	return c.Access == AccessFree && (c.start == nil || !day.After(c.start.AddDate(0, 0, closeAfterDays)))
}

// StartFor is the day the challenge starts for someone joining on day.
func (c Challenge) StartFor(day time.Time) time.Time {
	if c.start != nil {
		return *c.start
	}
	return day
}

func (c *Challenge) check() error {
	if !validSlug.MatchString(c.Slug) {
		return fmt.Errorf("challenge slug %q: lowercase letters, digits and dashes, at most 31", c.Slug)
	}
	if c.Title == "" || c.Description == "" || len(c.Days) == 0 {
		return fmt.Errorf("challenge %s: title, description and days are required", c.Slug)
	}
	if c.Access != AccessFree && c.Access != AccessClub {
		return fmt.Errorf("challenge %s: unknown access %q", c.Slug, c.Access)
	}
	if c.StartsOn != "" {
		t, err := time.Parse(time.DateOnly, c.StartsOn)
		if err != nil {
			return fmt.Errorf("challenge %s: starts_on: %w", c.Slug, err)
		}
		c.start = &t
	}
	for i, d := range c.Days {
		if d.Title == "" || d.Task == "" || d.XP <= 0 {
			return fmt.Errorf("challenge %s, day %d: title, task and xp are required", c.Slug, i+1)
		}
		switch d.Check {
		case CheckManual:
		case CheckAuto:
			if err := d.Rule.check(); err != nil {
				return fmt.Errorf("challenge %s, day %d: %w", c.Slug, i+1, err)
			}
		default:
			return fmt.Errorf("challenge %s, day %d: unknown check %q", c.Slug, i+1, d.Check)
		}
	}
	return nil
}

// Catalog is the content of the game.
type Catalog struct {
	Quests     []Quest
	Challenges []Challenge
}

// LoadCatalog reads and checks the embedded content.
func LoadCatalog() (Catalog, error) {
	var c Catalog
	var err error
	if c.Quests, err = parseQuests(rawQuests); err != nil {
		return Catalog{}, err
	}
	files, err := fs.ReadDir(rawChallenges, "challenges")
	if err != nil {
		return Catalog{}, err
	}
	for _, f := range files {
		data, err := rawChallenges.ReadFile("challenges/" + f.Name())
		if err != nil {
			return Catalog{}, err
		}
		ch, err := parseChallenge(data)
		if err != nil {
			return Catalog{}, fmt.Errorf("%s: %w", f.Name(), err)
		}
		c.Challenges = append(c.Challenges, ch)
	}
	return c, nil
}

func parseQuests(data []byte) ([]Quest, error) {
	var quests []Quest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&quests); err != nil {
		return nil, fmt.Errorf("quests.yaml: %w", err)
	}
	seen := map[string]bool{}
	for _, q := range quests {
		if err := q.check(); err != nil {
			return nil, fmt.Errorf("quests.yaml: %w", err)
		}
		if seen[q.Slug] {
			return nil, fmt.Errorf("quests.yaml: %s is there twice", q.Slug)
		}
		seen[q.Slug] = true
	}
	return quests, nil
}

func parseChallenge(data []byte) (Challenge, error) {
	var c Challenge
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return Challenge{}, err
	}
	return c, c.check()
}

// Quest finds a quest by slug.
func (c Catalog) Quest(slug string) (Quest, bool) {
	for _, q := range c.Quests {
		if q.Slug == slug {
			return q, true
		}
	}
	return Quest{}, false
}

// Challenge finds a challenge by slug.
func (c Catalog) Challenge(slug string) (Challenge, bool) {
	for _, ch := range c.Challenges {
		if ch.Slug == slug {
			return ch, true
		}
	}
	return Challenge{}, false
}
