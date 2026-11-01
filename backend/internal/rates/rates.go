// Package rates keeps the official exchange rates of the Bank of Russia. They
// come out once a working day; on a weekend the latest one stands.
package rates

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/text/encoding/charmap"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

const (
	dailyURL     = "https://www.cbr.ru/scripts/XML_daily.asp"
	fetchTimeout = 20 * time.Second
	maxBody      = 1 << 20
)

// Codes are the currencies the service follows.
var Codes = []string{"USD", "EUR"}

// Rate is rubles for one unit of a currency on a date.
type Rate struct {
	Code  string
	Date  time.Time
	Value decimal.Decimal
}

// Store persists rates.
type Store interface {
	Save(ctx context.Context, rates []Rate) error
	// Latest returns the newest rate of each code that has one.
	Latest(ctx context.Context, codes []string) ([]Rate, error)
	// Dates lists the days with a rate of a code from a date on.
	Dates(ctx context.Context, code string, from time.Time) ([]time.Time, error)
}

// Backfill reaches this far back on start: an operation may be dated to a past
// day, and it is converted at the rate of that day.
const backfillDays = 30

// ErrNoRate: nothing is known about a currency yet.
var ErrNoRate = ledger.ErrNoRate

// Updater fetches the rates. Requests go directly: the Telegram proxy leads
// abroad, and the Bank of Russia answers from inside the country.
type Updater struct {
	store  Store
	client *http.Client
	url    string
	log    *slog.Logger
}

func NewUpdater(store Store, log *slog.Logger) *Updater {
	return &Updater{store: store, client: &http.Client{Timeout: fetchTimeout}, url: dailyURL, log: log}
}

// Update fetches today's rates. A failure is logged: the morning message goes
// out with the last known ones.
func (u *Updater) Update(ctx context.Context) {
	rates, err := u.fetch(ctx)
	if err == nil {
		err = u.store.Save(ctx, rates)
	}
	if err != nil {
		u.log.WarnContext(ctx, "update exchange rates", "err", err)
	}
}

// Backfill fetches the working days missing from the last month. On a day
// without a rate the Bank of Russia answers with the latest one before it.
func (u *Updater) Backfill(ctx context.Context, today time.Time) {
	known, err := u.store.Dates(ctx, Codes[0], today.AddDate(0, 0, -backfillDays))
	if err != nil {
		u.log.WarnContext(ctx, "backfill exchange rates", "err", err)
		return
	}
	for d := today.AddDate(0, 0, -backfillDays); d.Before(today); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Sunday || d.Weekday() == time.Monday || slices.ContainsFunc(known, d.Equal) {
			continue // the rate of a Saturday covers Sunday and Monday
		}
		items, err := u.fetchURL(ctx, u.url+"?date_req="+d.Format("02/01/2006"))
		if err == nil {
			err = u.store.Save(ctx, items)
		}
		if err != nil {
			u.log.WarnContext(ctx, "backfill exchange rates", "date", d.Format(time.DateOnly), "err", err)
			return
		}
	}
}

func (u *Updater) fetch(ctx context.Context) ([]Rate, error) { return u.fetchURL(ctx, u.url) }

func (u *Updater) fetchURL(ctx context.Context, url string) ([]Rate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "savepoint-bot/1.0")
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch rates: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch rates: status %d", resp.StatusCode)
	}
	return parse(io.LimitReader(resp.Body, maxBody))
}

// parse reads the XML of the Bank of Russia: windows-1251, "80,5400" with a
// comma, the value per Nominal units, the date as DD.MM.YYYY.
func parse(r io.Reader) ([]Rate, error) {
	var doc struct {
		Date   string `xml:"Date,attr"`
		Valute []struct {
			CharCode string `xml:"CharCode"`
			Nominal  string `xml:"Nominal"`
			Value    string `xml:"Value"`
		} `xml:"Valute"`
	}
	dec := xml.NewDecoder(r)
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		if !strings.EqualFold(charset, "windows-1251") {
			return nil, fmt.Errorf("unexpected charset %q", charset)
		}
		return charmap.Windows1251.NewDecoder().Reader(input), nil
	}
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse rates: %w", err)
	}
	date, err := time.Parse("02.01.2006", doc.Date)
	if err != nil {
		return nil, fmt.Errorf("parse rates date: %w", err)
	}
	var out []Rate
	for _, v := range doc.Valute {
		if !slices.Contains(Codes, v.CharCode) {
			continue
		}
		value, err := decimal.NewFromString(strings.ReplaceAll(v.Value, ",", "."))
		if err != nil {
			return nil, fmt.Errorf("parse rate of %s: %w", v.CharCode, err)
		}
		nominal, err := decimal.NewFromString(v.Nominal)
		if err != nil || !nominal.IsPositive() {
			return nil, fmt.Errorf("parse nominal of %s: %q", v.CharCode, v.Nominal)
		}
		out = append(out, Rate{Code: v.CharCode, Date: date, Value: value.Div(nominal).Round(4)})
	}
	if len(out) == 0 {
		return nil, errors.New("parse rates: no known currency in the answer")
	}
	return out, nil
}
