package rates

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/text/encoding/charmap"
)

const sample = `<?xml version="1.0" encoding="windows-1251"?>
<ValCurs Date="31.10.2026" name="Foreign Currency Market">
<Valute ID="R01235"><NumCode>840</NumCode><CharCode>USD</CharCode><Nominal>1</Nominal><Name>Доллар США</Name><Value>80,5400</Value></Valute>
<Valute ID="R01239"><NumCode>978</NumCode><CharCode>EUR</CharCode><Nominal>10</Nominal><Name>Евро</Name><Value>931,0000</Value></Valute>
<Valute ID="R01335"><NumCode>398</NumCode><CharCode>KZT</CharCode><Nominal>100</Nominal><Name>Тенге</Name><Value>15,3000</Value></Valute>
</ValCurs>`

type fakeStore struct{ saved []Rate }

func (f *fakeStore) Save(_ context.Context, r []Rate) error {
	f.saved = append(f.saved, r...)
	return nil
}

func (f *fakeStore) Latest(context.Context, []string) ([]Rate, error) { return f.saved, nil }

func TestUpdate(t *testing.T) {
	body, err := charmap.Windows1251.NewEncoder().String(sample)
	if err != nil {
		t.Fatal(err)
	}
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	store := &fakeStore{}
	u := NewUpdater(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	u.url = srv.URL
	status = http.StatusBadGateway
	u.Update(context.Background())
	if len(store.saved) != 0 {
		t.Fatalf("saved on a failure: %+v", store.saved)
	}
	status = http.StatusOK
	u.Update(context.Background())

	if len(store.saved) != 2 {
		t.Fatalf("saved = %+v", store.saved)
	}
	date := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	for i, want := range []Rate{
		{"USD", date, decimal.RequireFromString("80.54")},
		{"EUR", date, decimal.RequireFromString("93.1")}, // per one euro, not per ten
	} {
		got := store.saved[i]
		if got.Code != want.Code || !got.Date.Equal(want.Date) || !got.Value.Equal(want.Value) {
			t.Errorf("rate %d = %+v, want %+v", i, got, want)
		}
	}
}
