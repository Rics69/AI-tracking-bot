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

	ClubMembers int
	Package     Package
	Recorded    Recorded      // operations of the last 7 days by the way they came
	Sources     []SourceCount // where those who joined in the last 7 days came from, biggest first
}

// Package is the supply of LLM tokens. Cached tokens are not billed and do not count.
type Package struct {
	Tokens     int64 // its size; zero: no package set
	Spent      int64
	WeekTokens int64 // spent in the last 7 days
	WeekDays   int   // days of those with any usage: a new service has few
}

// Recorded counts operations by the way they were recorded.
type Recorded struct {
	Fast, AI, MiniApp int
}

// SourceCount is how many users came from a source.
type SourceCount struct {
	Name  string
	Users int
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

// Source computes a snapshot; today, monthStart and packageSince are calendar
// dates in the owner's timezone, now is the current moment.
type Source interface {
	Snapshot(ctx context.Context, now, today, monthStart, packageSince time.Time) (Snapshot, error)
}

// Render formats a snapshot as a Telegram message for the owner.
func Render(s Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Статистика · %s\n\n", s.At.Format("02.01.2006 15:04"))
	fmt.Fprintf(&b, "Пользователи: %s (приняли политику %s, заблокировано %s)\n",
		Num(int64(s.Users)), Num(int64(s.Consented)), Num(int64(s.Blocked)))
	fmt.Fprintf(&b, "• новые: %s за сутки, %s за неделю\n", Num(int64(s.NewDay)), Num(int64(s.NewWeek)))
	fmt.Fprintf(&b, "• активные: %s за сутки, %s за неделю\n\n", Num(int64(s.ActiveDay)), Num(int64(s.ActiveWeek)))

	fmt.Fprintf(&b, "Операции за неделю: %s", Num(int64(s.OperationsWeek)))
	if s.RecordersWeek > 0 {
		fmt.Fprintf(&b, " — %s на записавшего (их %s)", ratio(float64(s.OperationsWeek)/float64(s.RecordersWeek)), Num(int64(s.RecordersWeek)))
	}
	fmt.Fprintf(&b, "\nЛимит включили: %s · цели: %s · регулярные платежи: %s\n",
		Num(int64(s.WithPlan)), Num(int64(s.WithGoals)), Num(int64(s.WithPayments)))
	fmt.Fprintf(&b, "Семьи из двух и больше: %s (%s человек)\n\n", Num(int64(s.Families)), Num(int64(s.InFamilies)))

	if s.ClubMembers > 0 {
		fmt.Fprintf(&b, "В клубе: %s\n", Num(int64(s.ClubMembers)))
	}
	b.WriteString(recorded(s.Recorded))
	if len(s.Sources) > 0 {
		parts := make([]string, len(s.Sources))
		for i, src := range s.Sources {
			parts[i] = src.Name + " " + Num(int64(src.Users))
		}
		b.WriteString("Новые за неделю по источникам: " + strings.Join(parts, ", ") + "\n")
	}
	b.WriteString("\n")

	b.WriteString("GigaChat сегодня: " + usage(s.Today) + "\n")
	b.WriteString("За месяц: " + usage(s.Month))
	if s.Package.Tokens > 0 {
		b.WriteString("\n\n" + packageState(s.Package))
	}
	return b.String()
}

func recorded(r Recorded) string {
	total := r.Fast + r.AI + r.MiniApp
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("Записи за неделю: быстрые %s (%s%%), через ИИ %s, в приложении %s\n",
		Num(int64(r.Fast)), ratio(100*float64(r.Fast)/float64(total)), Num(int64(r.AI)), Num(int64(r.MiniApp)))
}

func packageState(p Package) string {
	left := max(p.Tokens-p.Spent, 0)
	text := fmt.Sprintf("Пакет токенов: израсходовано %s из %s (%s%%), осталось %s",
		Num(p.Spent), Num(p.Tokens), ratio(100*float64(p.Spent)/float64(p.Tokens)), Num(left))
	if p.WeekDays == 0 || p.WeekTokens == 0 {
		return text
	}
	perDay := p.WeekTokens / int64(p.WeekDays)
	return text + fmt.Sprintf("\nв день: %s, хватит примерно на %s дн.", Num(perDay), Num(left/max(perDay, 1)))
}

func usage(u Usage) string {
	if u.Replies == 0 {
		return "ответов не было"
	}
	return fmt.Sprintf("%s ответов, ошибок %s (%s%%), в среднем %s с\nтокены: %s запрос (из кэша %s), %s ответ",
		Num(int64(u.Replies)), Num(int64(u.Failures)), ratio(100*float64(u.Failures)/float64(u.Replies)),
		ratio(u.ReplyTime.Seconds()/float64(u.Replies)),
		Num(u.PromptTokens), Num(u.CachedTokens), Num(u.CompletionTokens))
}

// Num groups thousands with a space: 1200000 → "1 200 000". Counts are never negative.
func Num(n int64) string {
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
