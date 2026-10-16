// Command eval measures the quality of the expense agent against the real LLM.
//
// Every case in eval/cases.yaml is a short dialog played on a fresh in-memory
// store; the final state of the expenses, the tools called and the reply are
// checked. Run it after changing the prompt, the tools or the model:
//
//	GIGACHAT_AUTH_KEY=... go run ./cmd/eval -runs 3 -report eval/report.md
package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/config"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm/provider"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/logger"
)

func main() {
	casesPath := flag.String("cases", "eval/cases.yaml", "path to the case file")
	runs := flag.Int("runs", 1, "runs per case: LLM answers vary, several runs show stability")
	filter := flag.String("filter", "", "run only cases whose name or tag contains this text")
	model := flag.String("model", "", "override GIGACHAT_MODEL, e.g. to compare models")
	report := flag.String("report", "", "write a Markdown report to this file")
	verbose := flag.Bool("v", false, "print replies and tool calls of every run")
	minScore := flag.Float64("min", 0, "exit with code 1 if the pass rate is below this (0..1)")
	flag.Parse()

	if err := run(*casesPath, *runs, *filter, *model, *report, *verbose, *minScore); err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(1)
	}
}

func run(casesPath string, runs int, filter, model, reportPath string, verbose bool, minScore float64) error {
	cfg, err := config.LoadEval()
	if err != nil {
		return err
	}
	if model != "" {
		cfg.GigaChat.Model = model
	}
	log := logger.New(cfg.LogLevel, "text")

	cases, err := loadCases(casesPath)
	if err != nil {
		return err
	}
	cases = slices.DeleteFunc(cases, func(c Case) bool { return !matches(c, filter) })
	if len(cases) == 0 {
		return fmt.Errorf("no cases match %q", filter)
	}

	client, err := provider.New(cfg.LLM, log)
	if err != nil {
		return err
	}
	rn := runner{
		client: client,
		loc:    cfg.Location,
		cfg:    agent.Config{Location: cfg.Location, MaxSteps: cfg.MaxSteps},
		log:    log,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Printf("model %s · %d cases × %d runs\n\n", cfg.GigaChat.Model, len(cases), runs)
	var results [][]Result
	for _, c := range cases {
		var caseResults []Result
		for range runs {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r := rn.run(ctx, c)
			caseResults = append(caseResults, r)
			if verbose {
				printVerbose(r)
			}
		}
		results = append(results, caseResults)
		printCaseLine(caseResults)
	}

	s := summarize(results)
	fmt.Println()
	s.print()

	if reportPath != "" {
		if err := writeReport(reportPath, cfg.GigaChat.Model, runs, results, s); err != nil {
			return err
		}
		fmt.Println("\nreport:", reportPath)
	}
	if s.score() < minScore {
		return fmt.Errorf("pass rate %.0f%% is below %.0f%%", s.score()*100, minScore*100)
	}
	return nil
}

func matches(c Case, filter string) bool {
	if filter == "" || strings.Contains(c.Name, filter) {
		return true
	}
	return slices.ContainsFunc(c.Tags, func(t string) bool { return strings.Contains(t, filter) })
}

type summary struct {
	passed, total  int
	byTag          map[string][2]int // tag -> {passed, total}
	turns          int
	duration       time.Duration
	prompt, cached int
	completion     int
}

func summarize(results [][]Result) summary {
	s := summary{byTag: map[string][2]int{}}
	for _, rs := range results {
		for _, r := range rs {
			s.total++
			s.turns += len(r.Case.Turns)
			s.duration += r.Duration
			s.prompt += r.Usage.PromptTokens
			s.cached += r.Usage.CachedTokens
			s.completion += r.Usage.CompletionTokens
			ok := 0
			if r.Passed() {
				s.passed++
				ok = 1
			}
			for _, tag := range r.Case.Tags {
				t := s.byTag[tag]
				s.byTag[tag] = [2]int{t[0] + ok, t[1] + 1}
			}
		}
	}
	return s
}

func (s summary) score() float64 {
	if s.total == 0 {
		return 0
	}
	return float64(s.passed) / float64(s.total)
}

func (s summary) avgTurn() time.Duration {
	if s.turns == 0 {
		return 0
	}
	return (s.duration / time.Duration(s.turns)).Round(10 * time.Millisecond)
}

func (s summary) print() {
	fmt.Printf("pass rate: %d/%d (%.0f%%)\n", s.passed, s.total, s.score()*100)
	for _, tag := range sortedTags(s.byTag) {
		t := s.byTag[tag]
		fmt.Printf("  %-10s %d/%d\n", tag, t[0], t[1])
	}
	fmt.Printf("avg reply time: %s · tokens: prompt %d (cached %d), completion %d\n",
		s.avgTurn(), s.prompt, s.cached, s.completion)
}

func sortedTags(m map[string][2]int) []string {
	tags := make([]string, 0, len(m))
	for t := range m {
		tags = append(tags, t)
	}
	slices.SortFunc(tags, cmp.Compare)
	return tags
}

func printCaseLine(rs []Result) {
	passed := 0
	for _, r := range rs {
		if r.Passed() {
			passed++
		}
	}
	mark := "✓"
	if passed < len(rs) {
		mark = "✗"
	}
	fmt.Printf("%s %-45s %d/%d\n", mark, rs[0].Case.Name, passed, len(rs))
	for _, r := range rs {
		for _, f := range r.Failures {
			fmt.Printf("    - %s\n", f)
		}
	}
}

func printVerbose(r Result) {
	fmt.Printf("── %s (tools: %v)\n", r.Case.Name, r.Tools)
	for i, reply := range r.Replies {
		fmt.Printf("  > %s\n  < %s\n", r.Case.Turns[i], strings.ReplaceAll(reply, "\n", "\n    "))
	}
}

func writeReport(path, model string, runs int, results [][]Result, s summary) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Eval агента\n\nМодель: `%s` · %s · прогонов на кейс: %d\n\n", model, time.Now().Format("2006-01-02 15:04"), runs)
	fmt.Fprintf(&b, "**Итог: %d/%d (%.0f%%)** · среднее время ответа %s · токены: prompt %d (из кэша %d), completion %d\n\n",
		s.passed, s.total, s.score()*100, s.avgTurn(), s.prompt, s.cached, s.completion)
	b.WriteString("| Группа | Пройдено |\n|---|---|\n")
	for _, tag := range sortedTags(s.byTag) {
		t := s.byTag[tag]
		fmt.Fprintf(&b, "| %s | %d/%d |\n", tag, t[0], t[1])
	}
	b.WriteString("\n| Кейс | Результат | Ошибки |\n|---|---|---|\n")
	for _, rs := range results {
		passed := 0
		var fails []string
		for _, r := range rs {
			if r.Passed() {
				passed++
			}
			fails = append(fails, r.Failures...)
		}
		fmt.Fprintf(&b, "| %s | %d/%d | %s |\n", rs[0].Case.Name, passed, len(rs),
			strings.ReplaceAll(strings.Join(fails, "<br>"), "|", "\\|"))
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
