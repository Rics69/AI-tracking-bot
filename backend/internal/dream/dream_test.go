package dream

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestCompute(t *testing.T) {
	n := decimal.NewFromInt
	income := func(v int64) *decimal.Decimal { d := n(v); return &d }
	tests := []struct {
		name    string
		in      Input
		month   int64
		day     int64
		share   int
		verdict Verdict
	}{
		{"from the spec", Input{Price: n(300000), Months: 12, MonthlyIncome: income(60000)}, 25000, 833, 42, Stretch},
		{"laptop", Input{Price: n(120000), Months: 12, MonthlyIncome: income(60000)}, 10000, 333, 17, Ambitious},
		{"what is saved counts", Input{Price: n(120000), Saved: n(60000), Months: 12}, 5000, 167, 0, Unknown},
		{"exactly 10%", Input{Price: n(120000), Months: 12, MonthlyIncome: income(100000)}, 10000, 333, 10, Realistic},
		{"exactly 30%", Input{Price: n(360000), Months: 12, MonthlyIncome: income(100000)}, 30000, 1000, 30, Ambitious},
		{"no income", Input{Price: n(1000), Months: 2, MonthlyIncome: income(0)}, 500, 17, 0, Unknown},
		{"no months is one", Input{Price: n(1000)}, 1000, 33, 0, Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Compute(tt.in)
			if r.Done || !r.PerMonth.Equal(n(tt.month)) || !r.PerDay.Equal(n(tt.day)) || r.Share != tt.share || r.Verdict != tt.verdict {
				t.Errorf("Compute = %+v", r)
			}
		})
	}
	if r := Compute(Input{Price: n(100), Saved: n(100), Months: 3}); !r.Done {
		t.Errorf("all saved = %+v", r)
	}
}

func TestParseDeadline(t *testing.T) {
	today := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for text, want := range map[string]string{
		"1.12.2027":       "2027-12-01",
		"12.2027":         "2027-12-01",
		"2028":            "2028-12-31",
		"через 8 месяцев": "2027-07-01",
		"8 мес":           "2027-07-01",
		"2 года":          "2028-11-01",
		"за 3 года":       "2029-11-01",
		"Декабрь 2027":    "2027-12-01",
		"к декабрю":       "2026-12-01",
		"в мае":           "2027-05-01",
		"к марту 2028":    "2028-03-01",
		"март":            "2027-03-01",
		"ноября 2027":     "2027-11-01",
	} {
		got, ok := ParseDeadline(text, today)
		if !ok || got.Format(time.DateOnly) != want {
			t.Errorf("ParseDeadline(%q) = %s, %v, want %s", text, got.Format(time.DateOnly), ok, want)
		}
	}
	for _, text := range []string{"", "скоро", "31.02.2027", "13.2027", "1.01.2020", "2025", "0 месяцев", "завтра"} {
		if got, ok := ParseDeadline(text, today); ok {
			t.Errorf("ParseDeadline(%q) = %s", text, got)
		}
	}
}

func TestMonths(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		today, deadline time.Time
		want            int
	}{
		{d(2026, 11, 1), d(2027, 11, 1), 12},
		{d(2026, 11, 1), d(2027, 11, 2), 13},
		{d(2026, 11, 20), d(2026, 12, 5), 1},
		{d(2026, 11, 20), d(2026, 11, 25), 1},
		{d(2026, 12, 31), d(2027, 1, 1), 1},
	}
	for _, tt := range tests {
		if got := Months(tt.today, tt.deadline); got != tt.want {
			t.Errorf("Months(%s, %s) = %d, want %d", tt.today.Format(time.DateOnly), tt.deadline.Format(time.DateOnly), got, tt.want)
		}
	}
}
