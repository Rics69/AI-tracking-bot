package report

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/stats"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
)

// kindPackage is the Runs kind of alerts about the token package; the period
// is the package's start and the level, so a new package alerts anew.
const kindPackage = "llm_package"

// packageLevels are the shares of the package the owner hears about, in percent.
var packageLevels = []int{50, 80, 95, 100}

// Spending tells how much of the token package is gone; *usage.Meter implements it.
type Spending interface {
	Spent(ctx context.Context) (int64, error)
}

// PackageAlert tells the owner when the token package is running out.
type PackageAlert struct {
	spending Spending
	runs     Runs
	send     SendFunc
	owner    int64
	tokens   int64
	since    time.Time
	t        texts.Bot
	log      *slog.Logger
}

func NewPackageAlert(spending Spending, runs Runs, send SendFunc, owner, tokens int64, since time.Time, t texts.Bot, log *slog.Logger) *PackageAlert {
	return &PackageAlert{spending: spending, runs: runs, send: send, owner: owner, tokens: tokens, since: since, t: t, log: log}
}

// Tick sends the alerts of the levels passed since the last one, each once.
func (a *PackageAlert) Tick(ctx context.Context) {
	spent, err := a.spending.Spent(ctx)
	if err != nil {
		a.log.ErrorContext(ctx, "package alert: spent tokens", "err", err)
		return
	}
	percent := int(spent * 100 / a.tokens)
	for _, level := range packageLevels {
		if percent < level {
			return
		}
		if err := a.alert(ctx, level, percent, spent); err != nil {
			a.log.ErrorContext(ctx, "package alert", "level", level, "err", err)
			return
		}
	}
}

func (a *PackageAlert) alert(ctx context.Context, level, percent int, spent int64) error {
	period := fmt.Sprintf("%s:%d", a.since.Format(time.DateOnly), level)
	claimed, err := a.runs.Claim(ctx, kindPackage, period, a.owner)
	if err != nil || !claimed {
		return err
	}
	text := a.t.PackageAlert
	if level == 100 {
		text = a.t.PackageOut
	}
	text = texts.Fill(text, "percent", strconv.Itoa(percent), "spent", stats.Num(spent),
		"total", stats.Num(a.tokens), "left", stats.Num(max(a.tokens-spent, 0)))
	if err := a.send(ctx, a.owner, text); err != nil {
		if relErr := a.runs.Release(ctx, kindPackage, period, a.owner); relErr != nil {
			a.log.ErrorContext(ctx, "package alert: release claim", "err", relErr)
		}
		return err
	}
	a.log.InfoContext(ctx, "package alert sent", "level", level, "spent", spent)
	return nil
}
