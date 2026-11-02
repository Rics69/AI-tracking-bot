package access_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/access"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/memory"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/usage"
)

const anna int64 = 1

var ctx = context.Background()

func setup() (*access.Service, *time.Time) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	s := access.New(memory.NewAccess(), memory.NewSettings(), usage.Limits{Messages: 150, Tokens: 1000}, usage.Limits{Messages: 300, Tokens: 5000})
	access.SetClock(s, func() time.Time { return now })
	return s, &now
}

func TestGrantAndExpiry(t *testing.T) {
	s, now := setup()
	if s.Has(ctx, anna, access.FeatureLibrary) {
		t.Fatal("nobody has the club by default")
	}
	end, err := s.Grant(ctx, anna, access.SourceAdmin, 30)
	if err != nil || !end.Equal(now.AddDate(0, 0, 30)) {
		t.Fatalf("grant = %s, %v", end, err)
	}
	if !s.Has(ctx, anna, access.FeatureLibrary) {
		t.Fatal("the grant is seen at once")
	}
	// Days add to what is left.
	end, _ = s.Grant(ctx, anna, access.SourcePromo, 7)
	if !end.Equal(now.AddDate(0, 0, 37)) {
		t.Errorf("second grant = %s", end)
	}
	*now = now.AddDate(0, 0, 38)
	if s.Has(ctx, anna, access.FeatureClubChat) {
		t.Error("the club outlived its end")
	}
}

func TestRevoke(t *testing.T) {
	s, _ := setup()
	if _, err := s.Grant(ctx, anna, access.SourceAdmin, 30); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, anna); err != nil {
		t.Fatal(err)
	}
	if s.Has(ctx, anna, access.FeaturePro) {
		t.Error("still in the club")
	}
}

func TestPromo(t *testing.T) {
	s, now := setup()
	code, err := s.NewPromo(ctx, "", 14, 2)
	if err != nil || code == "" {
		t.Fatalf("code = %q, %v", code, err)
	}
	end, err := s.Redeem(ctx, anna, " "+code+" ")
	if err != nil || !end.Equal(now.AddDate(0, 0, 14)) {
		t.Fatalf("redeem = %s, %v", end, err)
	}
	if _, err := s.Redeem(ctx, anna, code); !errors.Is(err, access.ErrPromoUsed) {
		t.Errorf("twice: %v", err)
	}
	if _, err := s.Redeem(ctx, 2, code); err != nil {
		t.Errorf("second user: %v", err)
	}
	if _, err := s.Redeem(ctx, 3, code); !errors.Is(err, access.ErrPromoInvalid) {
		t.Errorf("over the limit: %v", err)
	}
	if _, err := s.Redeem(ctx, 3, "NOPE"); !errors.Is(err, access.ErrPromoInvalid) {
		t.Errorf("unknown: %v", err)
	}
}

func TestLimitsByTierChangeWithoutARelease(t *testing.T) {
	s, _ := setup()
	if l, _ := s.Limits(ctx, anna); l.Messages != 150 {
		t.Errorf("free = %+v", l)
	}
	if err := s.SetLimits(ctx, access.TierFree, usage.Limits{Messages: 30, Tokens: 500}); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.Limits(ctx, anna); l.Messages != 30 || l.Tokens != 500 {
		t.Errorf("free after the change = %+v", l)
	}
	if _, err := s.Grant(ctx, anna, access.SourceAdmin, 30); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.Limits(ctx, anna); l.Messages != 300 {
		t.Errorf("club = %+v", l)
	}
}
