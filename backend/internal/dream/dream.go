// Package dream is the calculator of what a goal costs per month and per day.
// It is arithmetic for the user to act on, so it is done by code, not by the LLM.
package dream

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Verdict tells how a monthly saving compares with the income.
type Verdict int

const (
	Unknown   Verdict = iota // the income is not known
	Realistic                // up to 10% of the income
	Ambitious                // up to 30%
	Stretch                  // needs a bigger income or more time
)

const (
	realisticShare = 10
	ambitiousShare = 30
	daysInMonth    = 30
)

// Input is what the calculator asks.
type Input struct {
	Price         decimal.Decimal
	Saved         decimal.Decimal
	Months        int              // at least 1
	MonthlyIncome *decimal.Decimal // nil: unknown
}

// Result is the answer, in whole rubles. Done means the money is already there.
type Result struct {
	Done     bool
	PerMonth decimal.Decimal
	PerDay   decimal.Decimal
	Share    int // percent of the income, when the income is known
	Verdict  Verdict
}

// Compute works out the saving.
func Compute(in Input) Result {
	left := in.Price.Sub(in.Saved)
	if !left.IsPositive() {
		return Result{Done: true}
	}
	month := left.Div(decimal.NewFromInt(int64(max(in.Months, 1))))
	r := Result{
		PerMonth: month.Round(0),
		PerDay:   month.Div(decimal.NewFromInt(daysInMonth)).Round(0),
	}
	if in.MonthlyIncome == nil || !in.MonthlyIncome.IsPositive() {
		return r
	}
	share := month.Mul(decimal.NewFromInt(100)).Div(*in.MonthlyIncome)
	r.Share = int(share.Round(0).IntPart())
	switch {
	case share.LessThanOrEqual(decimal.NewFromInt(realisticShare)):
		r.Verdict = Realistic
	case share.LessThanOrEqual(decimal.NewFromInt(ambitiousShare)):
		r.Verdict = Ambitious
	default:
		r.Verdict = Stretch
	}
	return r
}

// Months counts the calendar months from today to a deadline, a started one
// counting as whole, and at least one.
func Months(today, deadline time.Time) int {
	n := (deadline.Year()-today.Year())*12 + int(deadline.Month()) - int(today.Month())
	if deadline.Day() > today.Day() {
		n++
	}
	return max(n, 1)
}

var (
	fullDate   = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})\.(\d{4})$`)
	monthYear  = regexp.MustCompile(`^(\d{1,2})\.(\d{4})$`)
	yearOnly   = regexp.MustCompile(`^(\d{4})$`)
	spanOfTime = regexp.MustCompile(`^(?:через |за )?(\d{1,3}) (мес|месяц|месяца|месяцев|год|года|лет)\.?$`)
	namedMonth = regexp.MustCompile(`^(?:к |в |до )?([а-я]+)(?: (\d{4}))?(?: года| г\.?)?$`)
)

// monthStems start the names of the months in every case: "декабрь",
// "декабря", "декабрю".
var monthStems = []string{"январ", "феврал", "март", "апрел", "ма", "июн", "июл", "август", "сентябр", "октябр", "ноябр", "декабр"}

// ParseDeadline reads a date a person would write: "1.12.2027", "12.2027",
// "2028", "через 8 месяцев", "2 года", "декабрь 2027", "к марту". The date must
// be after today.
func ParseDeadline(text string, today time.Time) (time.Time, bool) {
	s := strings.Join(strings.Fields(strings.ToLower(text)), " ")
	d, ok := deadline(s, today)
	return d, ok && d.After(today)
}

func deadline(s string, today time.Time) (time.Time, bool) {
	number := func(v string) int { n, _ := strconv.Atoi(v); return n }
	date := func(y, m, d int) (time.Time, bool) {
		t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
		return t, t.Day() == d && int(t.Month()) == m
	}
	if m := fullDate.FindStringSubmatch(s); m != nil {
		return date(number(m[3]), number(m[2]), number(m[1]))
	}
	if m := monthYear.FindStringSubmatch(s); m != nil {
		return date(number(m[2]), number(m[1]), 1)
	}
	if m := yearOnly.FindStringSubmatch(s); m != nil {
		return date(number(m[1]), 12, 31)
	}
	if m := spanOfTime.FindStringSubmatch(s); m != nil {
		n := number(m[1])
		if strings.HasPrefix(m[2], "мес") {
			return today.AddDate(0, n, 0), n > 0
		}
		return today.AddDate(n, 0, 0), n > 0
	}
	if m := namedMonth.FindStringSubmatch(s); m != nil {
		month := monthOf(m[1])
		if month == 0 {
			return time.Time{}, false
		}
		if m[2] != "" {
			return date(number(m[2]), month, 1)
		}
		d, _ := date(today.Year(), month, 1)
		if !d.After(today) {
			d = d.AddDate(1, 0, 0)
		}
		return d, true
	}
	return time.Time{}, false
}

func monthOf(word string) int {
	for i, stem := range monthStems {
		// "май", "мая", "маю" and "мае" are short enough to be matched exactly.
		if stem == "ма" {
			if slices.Contains([]string{"май", "мая", "маю", "мае"}, word) {
				return i + 1
			}
			continue
		}
		if strings.HasPrefix(word, stem) {
			return i + 1
		}
	}
	return 0
}
