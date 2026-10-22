// Package initdata validates Telegram Mini App init data: the query string
// Telegram.WebApp.initData, signed with the bot token. Only data that passes
// the check says who the user is.
//
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
package initdata

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrInvalid: malformed data or a wrong signature.
	ErrInvalid = errors.New("init data is invalid")
	// ErrExpired: the signature is right, but the data is too old to trust.
	ErrExpired = errors.New("init data has expired")
)

// User is the Telegram user who opened the Mini App.
type User struct {
	ID           int64  `json:"id"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
}

// Data is validated init data.
type Data struct {
	User       User
	AuthDate   time.Time
	StartParam string
}

// Parse checks the signature of raw init data with the bot token and returns
// its content. Data signed more than maxAge ago is rejected with ErrExpired.
func Parse(raw, botToken string, now time.Time, maxAge time.Duration) (Data, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return Data{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	hash := values.Get("hash")
	if hash == "" {
		return Data{}, fmt.Errorf("%w: no hash", ErrInvalid)
	}
	got, err := hex.DecodeString(hash)
	if err != nil {
		return Data{}, fmt.Errorf("%w: hash is not hex", ErrInvalid)
	}
	// The docs sign "all received fields" except the hash; the newer signature
	// field is accepted both ways, should Telegram leave it out.
	if !hmac.Equal(got, sign(values, botToken, "hash")) && !hmac.Equal(got, sign(values, botToken, "hash", "signature")) {
		return Data{}, fmt.Errorf("%w: wrong signature", ErrInvalid)
	}

	sec, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil {
		return Data{}, fmt.Errorf("%w: bad auth_date", ErrInvalid)
	}
	authDate := time.Unix(sec, 0)
	if now.Sub(authDate) > maxAge {
		return Data{}, ErrExpired
	}

	var u User
	if err := json.Unmarshal([]byte(values.Get("user")), &u); err != nil || u.ID <= 0 {
		return Data{}, fmt.Errorf("%w: no user", ErrInvalid) // e.g. opened from inline mode
	}
	return Data{User: u, AuthDate: authDate, StartParam: values.Get("start_param")}, nil
}

// sign computes the HMAC of the data-check-string: the fields except the
// excluded ones, sorted by key, as "key=value" lines.
func sign(values url.Values, botToken string, exclude ...string) []byte {
	keys := make([]string, 0, len(values))
	for k := range values {
		if !slices.Contains(exclude, k) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k + "=" + values.Get(k)
	}
	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	return hmacSHA256(secret, []byte(strings.Join(lines, "\n")))
}

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// Sign returns init data for user signed with botToken, as Telegram would
// produce it. It is for tests and local tools, never for real requests.
func Sign(u User, authDate time.Time, botToken string) string {
	userJSON, _ := json.Marshal(u)
	values := url.Values{
		"auth_date": {strconv.FormatInt(authDate.Unix(), 10)},
		"query_id":  {"AAH-test"},
		"user":      {string(userJSON)},
	}
	values.Set("hash", hex.EncodeToString(sign(values, botToken, "hash")))
	return values.Encode()
}
