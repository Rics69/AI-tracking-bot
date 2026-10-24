// Package stats is the owner's view of the service: how many people use it,
// what they use and what the LLM costs. Only counts, never anyone's data.
package stats

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Snapshot is the state of the service at a moment.
type Snapshot struct {
	At time.Time // in the owner's timezone

	Users      int // everyone who wrote to the bot or opened the app
	Consented  int
	Blocked    int
	NewDay     int // joined in the last 24 hours
	NewWeek    int // ... 7 days
	ActiveDay  int // wrote to the bot or opened the app in the last 24 hours
	ActiveWeek int

	OperationsWeek int // recorded in the last 7 days
	RecordersWeek  int // users who recorded them

	WithPlan     int // set a payday: the daily limit is on
	WithGoals    int
	WithPayments int // have regular payments
	Families     int // families of two or more
	InFamilies   int // their members

	Today Usage // the LLM today and this month, in the owner's timezone
	Month Usage
}

// Usage is what the agent cost in a period.
type Usage struct {
	Replies          int
	Failures         int
	PromptTokens     int64
	CachedTokens     int64
	CompletionTokens int64
	ReplyTime        time.Duration // total
}

// Source computes a snapshot; today and monthStart are calendar dates in the
// owner's timezone, now is the current moment.
type Source interface {
	Snapshot(ctx context.Context, now, today, monthStart time.Time) (Snapshot, error)
}

// Render formats a snapshot as a Telegram message for the owner.
func Render(s Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Статистика · %s\n\n", s.At.Format("02.01.2006 15:04"))
	fmt.Fprintf(&b, "Пользователи: %s (приняли политику %s, заблокировано %s)\n",
		num(int64(s.Users)), num(int64(s.Consented)), num(int64(s.Blocked)))
	fmt.Fprintf(&b, "• новые: %s за сутки, %s за неделю\n", num(int64(s.NewDay)), num(int64(s.NewWeek)))
	fmt.Fprintf(&b, "• активные: %s за сутки, %s за неделю\n\n", num(int64(s.ActiveDay)), num(int64(s.ActiveWeek)))

	fmt.Fprintf(&b, "Операции за неделю: %s", num(int64(s.OperationsWeek)))
	if s.RecordersWeek > 0 {
		fmt.Fprintf(&b, " — %s на записавшего (их %s)", ratio(float64(s.OperationsWeek)/float64(s.RecordersWeek)), num(int64(s.RecordersWeek)))
	}
	fmt.Fprintf(&b, "\nЛимит включили: %s · цели: %s · регулярные платежи: %s\n",
		num(int64(s.WithPlan)), num(int64(s.WithGoals)), num(int64(s.WithPayments)))
	fmt.Fprintf(&b, "Семьи из двух и больше: %s (%s человек)\n\n", num(int64(s.Families)), num(int64(s.InFamilies)))

	b.WriteString("GigaChat сегодня: " + usage(s.Today) + "\n")
	b.WriteString("За месяц: " + usage(s.Month))
	return b.String()
}

func usage(u Usage) string {
	if u.Replies == 0 {
		return "ответов не было"
	}
	return fmt.Sprintf("%s ответов, ошибок %s (%s%%), в среднем %s с\nтокены: %s запрос (из кэша %s), %s ответ",
		num(int64(u.Replies)), num(int64(u.Failures)), ratio(100*float64(u.Failures)/float64(u.Replies)),
		ratio(u.ReplyTime.Seconds()/float64(u.Replies)),
		num(u.PromptTokens), num(u.CachedTokens), num(u.CompletionTokens))
}

// num groups thousands with a space: 1200000 → "1 200 000". Counts are never negative.
func num(n int64) string {
	digits := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ratio prints one decimal with a comma: 7.25 → "7,3".
func ratio(f float64) string {
	return strings.Replace(strconv.FormatFloat(f, 'f', 1, 64), ".", ",", 1)
}
