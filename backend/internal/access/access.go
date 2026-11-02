// Package access answers one question: may this user use this feature. Every
// feature behind the club asks Has and nothing else, so how the access was
// obtained (the owner, a promo code, referrals, later a payment) stays here.
package access

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/usage"
)

// Products a user can have.
const ProductClub = "club"

// Features behind the club.
const (
	FeaturePro            = "pro"
	FeatureLibrary        = "library"
	FeatureClubChat       = "club_chat"
	FeatureClubChallenges = "club_challenges"
	FeatureStreakFreeze   = "streak_freeze"
)

// Sources of an entitlement.
const (
	SourceAdmin    = "admin"
	SourcePromo    = "promo"
	SourceReferral = "referral"
)

const (
	cacheFor     = time.Minute
	promoBytes   = 6
	day          = 24 * time.Hour
	ReferralDays = 7 // of the club, for every ReferralsForClub friends
	// ReferralsForClub is how many friends who finished onboarding earn the club.
	ReferralsForClub = 3
)

var (
	// ErrPromoInvalid: no such code, or it is over.
	ErrPromoInvalid = errors.New("promo code is not valid")
	// ErrPromoUsed: this user has used the code already.
	ErrPromoUsed = errors.New("promo code is already used")
)

// Entitlement is a period of access to a product.
type Entitlement struct {
	ID        int64
	UserID    int64
	Product   string
	Source    string
	StartsAt  time.Time
	ExpiresAt time.Time
}

// Promo is what a code gives.
type Promo struct {
	Hash      []byte
	Product   string
	Days      int
	MaxUses   int
	ExpiresAt *time.Time
}

// Ending is when a user's access runs out.
type Ending struct {
	UserID int64
	At     time.Time
}

// Store persists entitlements and promo codes.
type Store interface {
	Add(ctx context.Context, e Entitlement) error
	// Until is the end of the user's latest period of a product; nil: never had it.
	Until(ctx context.Context, userID int64, product string) (*time.Time, error)
	// Close ends all periods of a product now.
	Close(ctx context.Context, userID int64, product string, at time.Time) error
	// Endings lists the latest end per user, from a moment on.
	Endings(ctx context.Context, product string, from time.Time) ([]Ending, error)
	Active(ctx context.Context, product string, at time.Time) (int, error)

	AddPromo(ctx context.Context, p Promo) error
	// RedeemPromo uses a code once per user and returns what it gives.
	RedeemPromo(ctx context.Context, hash []byte, userID int64, at time.Time) (Promo, error)
}

// Settings keep what the owner changes without a release; family.Service's
// repository implements it.
type Settings interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
}

type cached struct {
	until   *time.Time
	checked time.Time
}

// Service is access to the paid part of the service.
type Service struct {
	store    Store
	settings Settings
	free     usage.Limits // when settings say nothing
	club     usage.Limits
	now      func() time.Time

	mu    sync.Mutex
	cache map[int64]cached
}

// New takes the limits used until the owner sets others.
func New(store Store, settings Settings, free, club usage.Limits) *Service {
	return &Service{store: store, settings: settings, free: free, club: club, now: time.Now, cache: map[int64]cached{}}
}

// Now is the clock of the service.
func (s *Service) Now() time.Time { return s.now() }

// Until is the end of the user's club, nil if they never had it.
func (s *Service) Until(ctx context.Context, userID int64) (*time.Time, error) {
	s.mu.Lock()
	c, ok := s.cache[userID]
	s.mu.Unlock()
	now := s.now()
	if ok && now.Sub(c.checked) < cacheFor {
		return c.until, nil
	}
	until, err := s.store.Until(ctx, userID, ProductClub)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cache[userID] = cached{until: until, checked: now}
	s.mu.Unlock()
	return until, nil
}

// Has reports whether the user may use a feature. All features come with the
// club for now, and a failed check means no: paid features are never free by
// accident, and nothing basic depends on them.
func (s *Service) Has(ctx context.Context, userID int64, _ string) bool {
	until, err := s.Until(ctx, userID)
	return err == nil && until != nil && until.After(s.now())
}

func (s *Service) forget(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, userID)
}

// Grant gives days of the club. They add to what is left, so nobody loses a
// day to a promo code.
func (s *Service) Grant(ctx context.Context, userID int64, source string, days int) (time.Time, error) {
	now := s.now()
	start := now
	until, err := s.store.Until(ctx, userID, ProductClub)
	if err != nil {
		return time.Time{}, err
	}
	if until != nil && until.After(now) {
		start = *until
	}
	end := start.Add(time.Duration(days) * day)
	err = s.store.Add(ctx, Entitlement{UserID: userID, Product: ProductClub, Source: source, StartsAt: start, ExpiresAt: end})
	s.forget(userID)
	return end, err
}

// Revoke ends the club now.
func (s *Service) Revoke(ctx context.Context, userID int64) error {
	defer s.forget(userID)
	return s.store.Close(ctx, userID, ProductClub, s.now())
}

// Endings lists when access runs out for those whose latest end is after from.
func (s *Service) Endings(ctx context.Context, from time.Time) ([]Ending, error) {
	return s.store.Endings(ctx, ProductClub, from)
}

// Members counts who is in the club now.
func (s *Service) Members(ctx context.Context) (int, error) {
	return s.store.Active(ctx, ProductClub, s.now())
}

func hashCode(code string) []byte {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return sum[:]
}

// NewPromo creates a code; an empty code is generated. The code itself is
// shown once and only its hash stays.
func (s *Service) NewPromo(ctx context.Context, code string, days, maxUses int) (string, error) {
	if code == "" {
		b := make([]byte, promoBytes)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		code = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	}
	err := s.store.AddPromo(ctx, Promo{Hash: hashCode(code), Product: ProductClub, Days: days, MaxUses: maxUses})
	return code, err
}

// Redeem turns a code into days of the club.
func (s *Service) Redeem(ctx context.Context, userID int64, code string) (time.Time, error) {
	p, err := s.store.RedeemPromo(ctx, hashCode(code), userID, s.now())
	if err != nil {
		return time.Time{}, err
	}
	return s.Grant(ctx, userID, SourcePromo, p.Days)
}

// Tiers of limits.
const (
	TierFree = "free"
	TierClub = "club"
)

func limitKeys(tier string) (messages, tokens string) {
	return "limit_" + tier + "_messages", "limit_" + tier + "_tokens"
}

func (s *Service) tier(ctx context.Context, tier string, def usage.Limits) (usage.Limits, error) {
	messages, tokens := limitKeys(tier)
	if v, ok, err := s.settings.Get(ctx, messages); err != nil {
		return def, err
	} else if ok {
		if n, err := strconv.Atoi(v); err == nil {
			def.Messages = n
		}
	}
	if v, ok, err := s.settings.Get(ctx, tokens); err != nil {
		return def, err
	} else if ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			def.Tokens = n
		}
	}
	return def, nil
}

// Limits are today's limits of a user: by their tier, read from the settings
// on every call, so the owner changes them without a release. The club has a
// ceiling too: the model is paid for by the service.
func (s *Service) Limits(ctx context.Context, userID int64) (usage.Limits, error) {
	if s.Has(ctx, userID, FeaturePro) {
		return s.tier(ctx, TierClub, s.club)
	}
	return s.tier(ctx, TierFree, s.free)
}

// TierLimits are the limits of a tier as they stand.
func (s *Service) TierLimits(ctx context.Context, tier string) (usage.Limits, error) {
	if tier == TierClub {
		return s.tier(ctx, tier, s.club)
	}
	return s.tier(ctx, TierFree, s.free)
}

// SetLimits changes the limits of a tier.
func (s *Service) SetLimits(ctx context.Context, tier string, l usage.Limits) error {
	messages, tokens := limitKeys(tier)
	if err := s.settings.Set(ctx, messages, strconv.Itoa(l.Messages)); err != nil {
		return err
	}
	return s.settings.Set(ctx, tokens, strconv.FormatInt(l.Tokens, 10))
}
