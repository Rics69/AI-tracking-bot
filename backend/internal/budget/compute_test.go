package budget

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func ptr[T any](v T) *T { return &v }

func TestPeriod(t *testing.T) {
	for _, tt := range []struct {
		name        string
		today       string
		paydays     []int
		start, next string
	}{
		{"mid-month", "2026-10-23", []int{5}, "2026-10-05", "2026-11-05"},
		{"payday today", "2026-10-05", []int{5}, "2026-10-05", "2026-11-05"},
		{"day before payday", "2026-10-04", []int{5}, "2026-09-05", "2026-10-05"},
		{"two paydays, second half", "2026-10-23", []int{5, 20}, "2026-10-20", "2026-11-05"},
		{"two paydays, first half", "2026-10-10", []int{5, 20}, "2026-10-05", "2026-10-20"},
		{"31st in February", "2026-02-15", []int{31}, "2026-01-31", "2026-02-28"},
		{"31st in February, payday", "2026-02-28", []int{31}, "2026-02-28", "2026-03-31"},
		{"31st in a leap February", "2028-02-29", []int{31}, "2028-02-29", "2028-03-31"},
		{"30th in a 31-day month", "2026-10-31", []int{30}, "2026-10-30", "2026-11-30"},
		{"new year", "2026-12-28", []int{25}, "2026-12-25", "2027-01-25"},
		{"before the first payday of the year", "2027-01-03", []int{10, 25}, "2026-12-25", "2027-01-10"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			start, next := Period(day(tt.today), tt.paydays)
			if !start.Equal(day(tt.start)) || !next.Equal(day(tt.next)) {
				t.Errorf("Period(%s, %v) = %s – %s, want %s – %s", tt.today, tt.paydays,
					start.Format(time.DateOnly), next.Format(time.DateOnly), tt.start, tt.next)
			}
		})
	}
}

// op builds an operation of the user on a date.
func op(date, amount string, c ledger.Category) ledger.Operation {
	return ledger.Operation{UserID: 1, Amount: dec(amount), Category: c, Date: day(date)}
}

func paying(o ledger.Operation, paymentID int64) ledger.Operation {
	o.RegularPaymentID = &paymentID
	return o
}

func TestCompute(t *testing.T) {
	rent := RegularPayment{ID: 7, Title: "Аренда", Amount: dec("30000"), Day: 25, Category: ledger.CategoryHousing,
		CreatedAt: day("2026-01-01")}
	phone := RegularPayment{ID: 8, Title: "Связь", Amount: dec("600"), Day: 1, Category: ledger.CategoryOther,
		CreatedAt: day("2026-01-01")}

	for _, tt := range []struct {
		name string
		in   Input
		// want
		daysLeft          int
		income            string
		payments, savings string
		spent, spentToday string
		available, limit  string
		ready, expected   bool
		upcoming          int
		overspent         bool
	}{
		{
			name: "no income: the limit is unknown",
			in: Input{Today: day("2026-10-23"), Plan: Plan{Paydays: []int{5}},
				Operations: []ledger.Operation{op("2026-10-20", "500", ledger.CategoryFood)}},
			daysLeft: 13, income: "0", payments: "0", savings: "0", spent: "500", spentToday: "0",
			available: "-500", limit: "0", ready: false,
		},
		{
			name: "recorded salary spread over the days left",
			in: Input{Today: day("2026-10-23"), Plan: Plan{Paydays: []int{5}},
				Operations: []ledger.Operation{
					op("2026-10-05", "80000", ledger.CategorySalary),
					op("2026-10-04", "999", ledger.CategoryFood), // before the period
					op("2026-10-10", "13000", ledger.CategoryFood),
				}},
			daysLeft: 13, income: "80000", payments: "0", savings: "0", spent: "13000", spentToday: "0",
			available: "67000", limit: "5153", ready: true,
		},
		{
			name: "payday today: the period starts today",
			in: Input{Today: day("2026-10-05"), Plan: Plan{Paydays: []int{5}},
				Operations: []ledger.Operation{op("2026-10-05", "62000", ledger.CategorySalary)}},
			daysLeft: 31, income: "62000", payments: "0", savings: "0", spent: "0", spentToday: "0",
			available: "62000", limit: "2000", ready: true,
		},
		{
			name: "expected income until the salary is recorded",
			in: Input{Today: day("2026-10-06"), Plan: Plan{Paydays: []int{5}, ExpectedIncome: ptr(dec("62000"))},
				Operations: []ledger.Operation{op("2026-10-05", "1000", ledger.CategoryGift)}},
			daysLeft: 30, income: "62000", payments: "0", savings: "0", spent: "0", spentToday: "0",
			available: "62000", limit: "2066", ready: true, expected: true,
		},
		{
			name: "recorded income above the expected one wins",
			in: Input{Today: day("2026-10-06"), Plan: Plan{Paydays: []int{5}, ExpectedIncome: ptr(dec("62000"))},
				Operations: []ledger.Operation{op("2026-10-05", "70000", ledger.CategorySalary)}},
			daysLeft: 30, income: "70000", payments: "0", savings: "0", spent: "0", spentToday: "0",
			available: "70000", limit: "2333", ready: true,
		},
		{
			name: "two paydays: only the half-month counts",
			in: Input{Today: day("2026-10-23"), Plan: Plan{Paydays: []int{5, 20}},
				Operations: []ledger.Operation{
					op("2026-10-05", "40000", ledger.CategorySalary),
					op("2026-10-20", "39000", ledger.CategorySalary),
					op("2026-10-21", "3000", ledger.CategoryFood),
				}},
			daysLeft: 13, income: "39000", payments: "0", savings: "0", spent: "3000", spentToday: "0",
			available: "36000", limit: "2769", ready: true,
		},
		{
			name: "today's spending does not change today's limit",
			in: Input{Today: day("2026-10-23"), Plan: Plan{Paydays: []int{5}},
				Operations: []ledger.Operation{
					op("2026-10-05", "65000", ledger.CategorySalary),
					op("2026-10-23", "9000", ledger.CategoryShopping),
				}},
			daysLeft: 13, income: "65000", payments: "0", savings: "0", spent: "0", spentToday: "9000",
			available: "65000", limit: "5000", ready: true, overspent: true,
		},
		{
			name: "regular payments are reserved, paid or not, and counted once",
			in: Input{Today: day("2026-10-23"), Plan: Plan{Paydays: []int{5}},
				Payments: []RegularPayment{rent, phone},
				Operations: []ledger.Operation{
					op("2026-10-05", "80000", ledger.CategorySalary),
					// The rent of the 25th was paid early, a bit more than planned.
					paying(op("2026-10-22", "31000", ledger.CategoryHousing), rent.ID),
					op("2026-10-22", "2000", ledger.CategoryFood),
				}},
			// Phone on Nov 1 is still due: 31000 + 600.
			daysLeft: 13, income: "80000", payments: "31600", savings: "0", spent: "2000", spentToday: "0",
			available: "46400", limit: "3569", ready: true, upcoming: 1,
		},
		{
			name: "a payment added mid-period is not owed for earlier dates",
			in: Input{Today: day("2026-10-23"), Plan: Plan{Paydays: []int{20}},
				Payments: []RegularPayment{{ID: 9, Title: "Кружок", Amount: dec("4000"), Day: 21,
					Category: ledger.CategoryOther, CreatedAt: day("2026-10-22")}},
				Operations: []ledger.Operation{op("2026-10-20", "50000", ledger.CategorySalary)}},
			daysLeft: 28, income: "50000", payments: "0", savings: "0", spent: "0", spentToday: "0",
			available: "50000", limit: "1785", ready: true,
		},
		{
			name: "goal contribution as planned, or what was actually saved if more",
			in: Input{Today: day("2026-10-23"), Plan: Plan{Paydays: []int{5}, GoalContribution: ptr(dec("10000"))},
				Saved:      dec("15000"),
				Operations: []ledger.Operation{op("2026-10-05", "80000", ledger.CategorySalary)}},
			daysLeft: 13, income: "80000", payments: "0", savings: "15000", spent: "0", spentToday: "0",
			available: "65000", limit: "5000", ready: true,
		},
		{
			name: "money taken back from a goal does not inflate the limit",
			in: Input{Today: day("2026-10-23"), Plan: Plan{Paydays: []int{5}}, Saved: dec("-5000"),
				Operations: []ledger.Operation{op("2026-10-05", "13000", ledger.CategorySalary)}},
			daysLeft: 13, income: "13000", payments: "0", savings: "0", spent: "0", spentToday: "0",
			available: "13000", limit: "1000", ready: true,
		},
		{
			name: "overspent period: negative available, zero limit",
			in: Input{Today: day("2026-10-23"), Plan: Plan{Paydays: []int{5}}, Payments: []RegularPayment{rent},
				Operations: []ledger.Operation{
					op("2026-10-05", "40000", ledger.CategorySalary),
					op("2026-10-12", "15000", ledger.CategoryShopping),
				}},
			daysLeft: 13, income: "40000", payments: "30000", savings: "0", spent: "15000", spentToday: "0",
			available: "-5000", limit: "0", ready: true, upcoming: 1,
		},
		{
			name: "31st payday in February: the period ends on the 28th",
			in: Input{Today: day("2026-02-27"), Plan: Plan{Paydays: []int{31}},
				Operations: []ledger.Operation{op("2026-01-31", "56000", ledger.CategorySalary)}},
			daysLeft: 1, income: "56000", payments: "0", savings: "0", spent: "0", spentToday: "0",
			available: "56000", limit: "56000", ready: true,
		},
		{
			name: "someone else's payment link is an ordinary expense",
			in: Input{Today: day("2026-10-23"), Plan: Plan{Paydays: []int{5}},
				Operations: []ledger.Operation{
					op("2026-10-05", "26000", ledger.CategorySalary),
					paying(op("2026-10-06", "13000", ledger.CategoryHousing), 404),
				}},
			daysLeft: 13, income: "26000", payments: "0", savings: "0", spent: "13000", spentToday: "0",
			available: "13000", limit: "1000", ready: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := Compute(tt.in)
			check := func(field string, got decimal.Decimal, want string) {
				if !got.Equal(dec(want)) {
					t.Errorf("%s = %s, want %s", field, got, want)
				}
			}
			if b.DaysLeft != tt.daysLeft {
				t.Errorf("DaysLeft = %d, want %d", b.DaysLeft, tt.daysLeft)
			}
			check("Income", b.Income, tt.income)
			check("Payments", b.Payments, tt.payments)
			check("Savings", b.Savings, tt.savings)
			check("Spent", b.Spent, tt.spent)
			check("SpentToday", b.SpentToday, tt.spentToday)
			check("Available", b.Available, tt.available)
			check("Limit", b.Limit, tt.limit)
			if b.Ready != tt.ready || b.IncomeExpected != tt.expected {
				t.Errorf("Ready, IncomeExpected = %v, %v, want %v, %v", b.Ready, b.IncomeExpected, tt.ready, tt.expected)
			}
			if len(b.Upcoming) != tt.upcoming {
				t.Errorf("Upcoming = %v, want %d", b.Upcoming, tt.upcoming)
			}
			if b.Overspent() != tt.overspent {
				t.Errorf("Overspent = %v, want %v", b.Overspent(), tt.overspent)
			}
		})
	}
}

func TestDue(t *testing.T) {
	rent := RegularPayment{Day: 1, Amount: dec("30000"), CreatedAt: day("2026-01-10")}
	for _, tt := range []struct {
		name     string
		today    string
		lastPaid string
		created  string
		want     string
	}{
		{"never paid, the date passed", "2026-10-23", "", "", "2026-10-01"},
		{"paid this month", "2026-10-23", "2026-10-01", "", "2026-11-01"},
		{"paid a few days early", "2026-10-23", "2026-09-28", "", "2026-11-01"},
		{"next month paid early too", "2026-10-29", "2026-10-28", "", "2026-12-01"},
		{"due today", "2026-11-01", "2026-10-01", "", "2026-11-01"},
		{"added after this month's date", "2026-10-23", "", "2026-10-20", "2026-11-01"},
		{"day 31 in a short month", "2026-11-15", "2026-10-31", "", "2026-11-30"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := rent
			if tt.lastPaid != "" {
				p.LastPaidOn = ptr(day(tt.lastPaid))
			}
			if tt.created != "" {
				p.CreatedAt = day(tt.created)
			}
			if tt.name == "day 31 in a short month" {
				p.Day = 31
			}
			if got := p.Due(day(tt.today)); !got.Equal(day(tt.want)) {
				t.Errorf("Due(%s) = %s, want %s", tt.today, got.Format(time.DateOnly), tt.want)
			}
		})
	}
}

func TestPlanValidation(t *testing.T) {
	for _, tt := range []struct {
		name  string
		plan  Plan
		field string
	}{
		{"ok, sorted and deduplicated", Plan{Paydays: []int{20, 5, 5}}, ""},
		{"no paydays", Plan{}, "paydays"},
		{"three paydays", Plan{Paydays: []int{1, 10, 20}}, "paydays"},
		{"day 32", Plan{Paydays: []int{32}}, "paydays"},
		{"zero income", Plan{Paydays: []int{5}, ExpectedIncome: ptr(decimal.Zero)}, "expected_income"},
		{"kopecks of kopecks", Plan{Paydays: []int{5}, GoalContribution: ptr(dec("1.005"))}, "goal_contribution"},
		{"hour 24", Plan{Paydays: []int{5}, ReminderHour: 24}, "reminder_hour"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.plan
			err := p.normalize()
			field := ""
			if verr, ok := err.(*ledger.ValidationError); ok {
				field = verr.Field
			} else if err != nil {
				t.Fatal(err)
			}
			if field != tt.field {
				t.Errorf("error field %q, want %q (%v)", field, tt.field, err)
			}
			if err == nil && (len(p.Paydays) != 2 || p.Paydays[0] != 5 || p.Paydays[1] != 20) {
				t.Errorf("paydays = %v", p.Paydays)
			}
		})
	}
}

func TestDueOn(t *testing.T) {
	rent := RegularPayment{Day: 31, Amount: dec("30000"), CreatedAt: day("2026-01-10")}
	for _, tt := range []struct {
		date, lastPaid string
		want           bool
	}{
		{"2026-11-30", "", true},            // the 31st in November is the 30th
		{"2026-11-29", "", false},           // not the day
		{"2026-11-30", "2026-10-31", true},  // last month's payment does not count
		{"2026-11-30", "2026-11-27", false}, // paid three days early
		{"2025-12-31", "", false},           // before the payment was added
	} {
		p := rent
		if tt.lastPaid != "" {
			p.LastPaidOn = ptr(day(tt.lastPaid))
		}
		if got := p.DueOn(day(tt.date)); got != tt.want {
			t.Errorf("DueOn(%s), paid %q = %v, want %v", tt.date, tt.lastPaid, got, tt.want)
		}
	}
}
