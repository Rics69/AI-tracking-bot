package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/initdata"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// InitDataMaxAge is how long signed init data is trusted: the Mini App gets
// new data each time it is opened.
const InitDataMaxAge = 24 * time.Hour

var (
	// ErrUnauthorized: no valid Telegram init data in the request.
	ErrUnauthorized = errors.New("open the app from Telegram: the request has no valid init data")
	// ErrConsentRequired: the user has not accepted the data policy yet.
	ErrConsentRequired = errors.New("accept the data policy first")
)

// Admitter finds or creates the user behind validated init data; *family.Service implements it.
type Admitter interface {
	Admit(ctx context.Context, tg user.User) (user.User, error)
	Get(ctx context.Context, telegramID int64) (user.User, error)
}

// Auth identifies the caller of every API request by Telegram init data in
// the "Authorization: tma <initData>" header.
type Auth struct {
	BotToken string
	Users    Admitter
	// DevUserID, if set, serves requests without init data as this user, so
	// the panel works in a plain browser. Only for APP_ENV=dev: see config.
	DevUserID int64
	Now       func() time.Time
	Log       *slog.Logger
}

type callerKey struct{}

// Middleware authenticates the request and puts the caller into its context.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := a.authenticate(r)
		if err != nil {
			writeError(a.Log, w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, u)))
	})
}

func (a *Auth) authenticate(r *http.Request) (user.User, error) {
	ctx := r.Context()
	header := r.Header.Get("Authorization")
	raw, ok := strings.CutPrefix(header, "tma ")
	if !ok {
		if header == "" && a.DevUserID != 0 {
			return a.devUser(ctx)
		}
		return user.User{}, ErrUnauthorized
	}
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	data, err := initdata.Parse(raw, a.BotToken, now(), InitDataMaxAge)
	if err != nil {
		a.Log.InfoContext(ctx, "init data rejected", "err", err)
		return user.User{}, ErrUnauthorized
	}
	// The same as a first message to the bot: a new user gets a family of their own.
	return a.Users.Admit(ctx, user.User{
		TelegramID: data.User.ID, FirstName: data.User.FirstName, Username: data.User.Username,
	})
}

// devUser is the developer's own account, created if they never wrote to the bot.
func (a *Auth) devUser(ctx context.Context) (user.User, error) {
	u, err := a.Users.Get(ctx, a.DevUserID)
	if errors.Is(err, user.ErrNotFound) {
		return a.Users.Admit(ctx, user.User{TelegramID: a.DevUserID, FirstName: "Разработчик"})
	}
	if err == nil && u.Blocked() {
		return user.User{}, ErrUnauthorized
	}
	return u, err
}

// caller returns the authenticated user of the request.
func caller(ctx context.Context) (user.User, error) {
	u, ok := ctx.Value(callerKey{}).(user.User)
	if !ok {
		return user.User{}, ErrUnauthorized
	}
	return u, nil
}

// consented returns the caller, who must have accepted the data policy.
func consented(ctx context.Context) (user.User, error) {
	u, err := caller(ctx)
	if err != nil {
		return user.User{}, err
	}
	if !u.Consented() {
		return user.User{}, ErrConsentRequired
	}
	return u, nil
}
