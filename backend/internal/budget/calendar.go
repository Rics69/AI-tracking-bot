package budget

import (
	"slices"
	"time"
)

// graceDays: a regular payment paid up to a week early counts for the coming date.
const graceDays = 7

// dayIn returns a day of a month; a day past the end of the month is its last day
// (payday 31 in February is the 28th or 29th).
func dayIn(year int, month time.Month, day int) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(year, month, min(day, last), 0, 0, 0, 0, time.UTC)
}

// Period returns the pay period that contains today: from the last payday on
// or before today to the next payday after it (exclusive).
func Period(today time.Time, paydays []int) (start, next time.Time) {
	var days []time.Time
	for offset := -1; offset <= 1; offset++ {
		m := time.Date(today.Year(), today.Month()+time.Month(offset), 1, 0, 0, 0, 0, time.UTC)
		for _, d := range paydays {
			days = append(days, dayIn(m.Year(), m.Month(), d))
		}
	}
	slices.SortFunc(days, time.Time.Compare)
	for _, d := range days {
		if d.After(today) {
			return start, d
		}
		start = d
	}
	return start, next // unreachable: next month always has a payday
}

// DaysLeft counts the days from today to the day before next, today included.
func DaysLeft(today, next time.Time) int {
	return int(next.Sub(today).Hours()/24 + 0.5)
}

// occurrences lists the dates of a monthly day in [from, to).
func occurrences(day int, from, to time.Time) []time.Time {
	var out []time.Time
	for m := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC); m.Before(to); m = m.AddDate(0, 1, 0) {
		if d := dayIn(m.Year(), m.Month(), day); !d.Before(from) && d.Before(to) {
			out = append(out, d)
		}
	}
	return out
}

// Due returns the date the payment is due next: the latest due date if it
// passed unpaid (today or earlier), otherwise the coming one. Dates before the
// payment was added are not owed.
func (p RegularPayment) Due(today time.Time) time.Time {
	cur := dayIn(today.Year(), today.Month(), p.Day)
	if cur.After(today) {
		prev := cur.AddDate(0, 0, -cur.Day()) // the last day of the previous month
		cur = dayIn(prev.Year(), prev.Month(), p.Day)
	}
	if !p.paidFor(cur) && !cur.Before(dateOf(p.CreatedAt)) {
		return cur
	}
	next := p.after(cur)
	if p.paidFor(next) {
		next = p.after(next)
	}
	return next
}

// DueOn reports whether the payment falls on date and is not paid for it yet:
// "tomorrow is rent day" reminders do not depend on earlier dates being marked paid.
func (p RegularPayment) DueOn(date time.Time) bool {
	return dayIn(date.Year(), date.Month(), p.Day).Equal(date) && !p.paidFor(date) && !date.Before(dateOf(p.CreatedAt))
}

// after returns the due date in the month after d's.
func (p RegularPayment) after(d time.Time) time.Time {
	m := time.Date(d.Year(), d.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	return dayIn(m.Year(), m.Month(), p.Day)
}

func (p RegularPayment) paidFor(due time.Time) bool {
	return p.LastPaidOn != nil && !p.LastPaidOn.Before(due.AddDate(0, 0, -graceDays))
}
