package stats

import (
	"strings"
	"testing"
	"time"
)

func TestRenderWithoutNews(t *testing.T) {
	text := Render(Snapshot{Package: Package{Tokens: 1000, Spent: 1200}})
	if !strings.Contains(text, "осталось 0") || strings.Contains(text, "хватит") {
		t.Errorf("a spent package:\n%s", text)
	}
	text = Render(Snapshot{})
	for _, absent := range []string{"Пакет", "Записи за неделю", "источникам"} {
		if strings.Contains(text, absent) {
			t.Errorf("stats show %q with nothing to say:\n%s", absent, text)
		}
	}
}

func TestRender(t *testing.T) {
	text := Render(Snapshot{
		At:    time.Date(2026, 10, 24, 14, 5, 0, 0, time.UTC),
		Users: 1280, Consented: 1200, Blocked: 1, NewDay: 5, NewWeek: 31, ActiveDay: 40, ActiveWeek: 85,
		OperationsWeek: 640, RecordersWeek: 80,
		WithPlan: 12, WithGoals: 9, WithPayments: 7, Families: 4, InFamilies: 11,
		Today: Usage{Replies: 200, Failures: 3, PromptTokens: 1200000, CachedTokens: 950000, CompletionTokens: 40000,
			ReplyTime: 840 * time.Second},
		Package:  Package{Tokens: 2500000, Spent: 500000, WeekTokens: 70000, WeekDays: 7},
		Recorded: Recorded{Fast: 30, AI: 15, MiniApp: 5},
		Sources:  []SourceCount{{"reels", 20}, {"direct", 11}},
	})
	for _, want := range []string{
		"Статистика · 24.10.2026 14:05",
		"Пользователи: 1 280 (приняли политику 1 200, заблокировано 1)",
		"• новые: 5 за сутки, 31 за неделю",
		"• активные: 40 за сутки, 85 за неделю",
		"Операции за неделю: 640 — 8,0 на записавшего (их 80)",
		"Лимит включили: 12 · цели: 9 · регулярные платежи: 7",
		"Семьи из двух и больше: 4 (11 человек)",
		"GigaChat сегодня: 200 ответов, ошибок 3 (1,5%), в среднем 4,2 с",
		"токены: 1 200 000 запрос (из кэша 950 000), 40 000 ответ",
		"За месяц: ответов не было",
		"Записи за неделю: быстрые 30 (60,0%), через ИИ 15, в приложении 5",
		"Новые за неделю по источникам: reels 20, direct 11",
		"Пакет токенов: израсходовано 500 000 из 2 500 000 (20,0%), осталось 2 000 000",
		"в день: 10 000, хватит примерно на 200 дн.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("stats lack %q:\n%s", want, text)
		}
	}
}
