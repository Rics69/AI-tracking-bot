// Package quick records plain "кофе 350" without the LLM: such messages are
// most of what people write, and they should cost no tokens and work even when
// the model does not. Only certainties go this way, everything else is the
// agent's: a wrong record is worse than a spent token.
package quick

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/report"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
)

//go:embed quick.yaml
var rawDictionary []byte

const (
	// closeRunes is how much of a word must match the name of a payment or a
	// goal: "аренду" is "Аренда", "кофе" may be "Кофемашина".
	closeRunes = 4
	// Doubtful amounts: "зарплата 5" is a payday, "такси 1.5" is 1500.
	minIncome     = 100
	minFractional = 10
	maxAmount     = 100_000_000
)

// sum is an amount as people write it; its groups are the whole part, the
// fraction and the multiplier.
const sum = `(\d{1,3}(?: \d{3})+|\d+)([.,]\d{1,2})? ?(к|тыс|тысяч|тысячи|млн|миллион|миллиона|миллионов)?\.? ?(?:₽|р|руб|рублей|рубля)?\.?`

var (
	// entry is the end of the message: a sum and maybe a day.
	entry = regexp.MustCompile(`^(.+?) ` + sum + `(?: (сегодня|вчера|позавчера))?$`)
	// bare is a message that is only a sum.
	bare = regexp.MustCompile(`^` + sum + `$`)
)

// Parsed is a message the parser is sure about.
type Parsed struct {
	Amount   decimal.Decimal
	Category ledger.Category
	Comment  string
	Date     time.Time
}

// Budgets is the part of budget.Service the check against payments and goals needs.
type Budgets interface {
	Goals(ctx context.Context, userID int64) ([]budget.Goal, error)
	Payments(ctx context.Context, userID int64) ([]budget.RegularPayment, error)
}

// Ledger is the part of ledger.Service the fast path writes through.
type Ledger interface {
	Create(ctx context.Context, in ledger.CreateInput) (ledger.Operation, error)
	Delete(ctx context.Context, s ledger.Scope, id int64) error
}

// Service records what the parser understands.
type Service struct {
	ledger     Ledger
	budgets    Budgets
	dictionary map[string]ledger.Category
}

// New reads the embedded dictionary.
func New(l Ledger, b Budgets) (*Service, error) {
	d, err := parseDictionary(rawDictionary)
	if err != nil {
		return nil, err
	}
	return &Service{ledger: l, budgets: b, dictionary: d}, nil
}

func parseDictionary(data []byte) (map[string]ledger.Category, error) {
	var byCategory map[ledger.Category][]string
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&byCategory); err != nil {
		return nil, fmt.Errorf("quick.yaml: %w", err)
	}
	d := map[string]ledger.Category{}
	for c, words := range byCategory {
		if !c.Valid() {
			return nil, fmt.Errorf("quick.yaml: unknown category %q", c)
		}
		for _, w := range words {
			key := fold(w)
			if other, ok := d[key]; ok {
				return nil, fmt.Errorf("quick.yaml: %q is in both %s and %s", w, other, c)
			}
			d[key] = c
		}
	}
	return d, nil
}

// fold is the form words are compared in.
func fold(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "ё", "е")
}

// Parse understands "<слово из словаря> <сумма> [сегодня|вчера|позавчера]".
func (s *Service) Parse(text string, today time.Time) (Parsed, bool) {
	m := entry.FindStringSubmatch(normalize(text))
	if m == nil {
		return Parsed{}, false
	}
	phrase, whole, fraction, multiplier, day := m[1], m[2], m[3], m[4], m[5]
	category, ok := s.dictionary[fold(phrase)]
	if !ok {
		return Parsed{}, false
	}
	amount, ok := toAmount(whole, fraction, multiplier)
	if !ok || !plausible(amount, category, fraction != "" && multiplier == "") {
		return Parsed{}, false
	}
	date := today
	switch day {
	case "вчера":
		date = today.AddDate(0, 0, -1)
	case "позавчера":
		date = today.AddDate(0, 0, -2)
	}
	return Parsed{Amount: amount, Category: category, Comment: phrase, Date: date}, true
}

// Amount reads a message that is only a sum: "120к", "1,5 млн", "45 000 ₽".
func Amount(text string) (decimal.Decimal, bool) {
	m := bare.FindStringSubmatch(normalize(text))
	if m == nil {
		return decimal.Zero, false
	}
	amount, ok := toAmount(m[1], m[2], m[3])
	return amount, ok && amount.IsPositive() && amount.LessThan(decimal.NewFromInt(maxAmount*10))
}

func toAmount(whole, fraction, multiplier string) (decimal.Decimal, bool) {
	amount, err := decimal.NewFromString(strings.ReplaceAll(whole, " ", "") + strings.ReplaceAll(fraction, ",", "."))
	if err != nil {
		return decimal.Zero, false
	}
	switch multiplier {
	case "":
	case "млн", "миллион", "миллиона", "миллионов":
		amount = amount.Mul(decimal.NewFromInt(1_000_000))
	default:
		amount = amount.Mul(decimal.NewFromInt(1000))
	}
	return amount.Round(2), true
}

// normalize lowers the case and collapses spaces, and drops the final dot.
func normalize(text string) string {
	line := strings.Join(strings.FieldsFunc(strings.ToLower(text), unicode.IsSpace), " ")
	return strings.TrimRight(line, ".!")
}

func plausible(amount decimal.Decimal, c ledger.Category, fractional bool) bool {
	switch {
	case !amount.IsPositive(), amount.GreaterThanOrEqual(decimal.NewFromInt(maxAmount)):
		return false
	case c.Kind() == ledger.KindIncome && amount.LessThan(decimal.NewFromInt(minIncome)):
		return false
	case fractional && amount.LessThan(decimal.NewFromInt(minFractional)):
		return false
	}
	return true
}

// Record writes the message down if it is certain. "аренда 45000" with a
// regular payment «Аренда» goes to the agent: it links the expense to the
// payment, otherwise the daily limit would count the rent twice.
func (s *Service) Record(ctx context.Context, userID int64, text string, today time.Time) (ledger.Operation, bool, error) {
	p, ok := s.Parse(text, today)
	if !ok {
		return ledger.Operation{}, false, nil
	}
	named, err := s.named(ctx, userID, p.Comment)
	if err != nil {
		return ledger.Operation{}, false, err
	}
	if named {
		return ledger.Operation{}, false, nil
	}
	op, err := s.ledger.Create(ctx, ledger.CreateInput{
		UserID: userID, Amount: p.Amount, Category: p.Category, Date: &p.Date, Comment: p.Comment, Today: today,
	})
	var invalid *ledger.ValidationError
	if errors.As(err, &invalid) {
		return ledger.Operation{}, false, nil
	}
	if err != nil {
		return ledger.Operation{}, false, err
	}
	return op, true, nil
}

// Delete undoes a record of the user: the button comes from the client, the
// scope decides.
func (s *Service) Delete(ctx context.Context, userID, id int64) error {
	return s.ledger.Delete(ctx, ledger.OwnedBy(userID), id)
}

// named reports whether the phrase looks like a regular payment or a goal of the user.
func (s *Service) named(ctx context.Context, userID int64, phrase string) (bool, error) {
	payments, err := s.budgets.Payments(ctx, userID)
	if err != nil {
		return false, err
	}
	goals, err := s.budgets.Goals(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, p := range payments {
		if similar(phrase, p.Title) {
			return true, nil
		}
	}
	for _, g := range goals {
		if similar(phrase, g.Title) {
			return true, nil
		}
	}
	return false, nil
}

// similar reports two names sharing a word, up to its ending.
func similar(a, b string) bool {
	for _, x := range strings.Fields(fold(a)) {
		for _, y := range strings.Fields(fold(b)) {
			if sameStem(x, y) {
				return true
			}
		}
	}
	return false
}

func sameStem(a, b string) bool {
	x, y := []rune(a), []rune(b)
	n := 0
	for n < len(x) && n < len(y) && x[n] == y[n] {
		n++
	}
	return n >= closeRunes || n == len(x) && n == len(y)
}

// Reply is the answer to a record, in the voice of the agent's answers.
func Reply(t texts.Bot, op ledger.Operation, today time.Time) string {
	text := t.QuickSaved
	if op.Kind() == ledger.KindIncome {
		text = t.QuickSavedIncome
	}
	return texts.Fill(text, "what", op.Comment, "amount", report.Money(op.Amount),
		"category", op.Category.Label(), "date", agent.HumanDate(op.Date, today))
}
