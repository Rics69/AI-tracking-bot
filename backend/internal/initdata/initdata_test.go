package initdata

import (
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

const token = "123456:test-token"

var (
	now  = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	anna = User{ID: 42, FirstName: "Анна", Username: "anna", LanguageCode: "ru"}
)

func TestValidData(t *testing.T) {
	d, err := Parse(Sign(anna, now.Add(-time.Hour), token), token, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if d.User != anna || !d.AuthDate.Equal(now.Add(-time.Hour)) {
		t.Errorf("data = %+v", d)
	}
}

// TestKnownVector pins the algorithm to a hand-computed example, so a
// symmetric bug in Sign and Parse cannot go unnoticed.
func TestKnownVector(t *testing.T) {
	raw := "auth_date=1761134400&query_id=AAH-test&user=%7B%22id%22%3A42%2C%22first_name%22%3A%22Anna%22%7D" +
		"&hash=" + knownHash
	if _, err := Parse(raw, token, time.Unix(1761134400, 0), time.Hour); err != nil {
		t.Fatal(err)
	}
}

func TestRejected(t *testing.T) {
	good := Sign(anna, now, token)
	tamper := func(key, value string) string {
		v, _ := url.ParseQuery(good)
		v.Set(key, value)
		return v.Encode()
	}
	for name, tt := range map[string]struct {
		raw  string
		want error
	}{
		"another bot":    {Sign(anna, now, "999:other"), ErrInvalid},
		"changed user":   {tamper("user", `{"id":1,"first_name":"Мэллори"}`), ErrInvalid},
		"changed date":   {tamper("auth_date", "1"), ErrInvalid},
		"no hash":        {strings.Split(good, "&hash=")[0], ErrInvalid},
		"garbage":        {"%%%", ErrInvalid},
		"empty":          {"", ErrInvalid},
		"too old":        {Sign(anna, now.Add(-25*time.Hour), token), ErrExpired},
		"no user inside": {Sign(User{}, now, token), ErrInvalid},
	} {
		if _, err := Parse(tt.raw, token, now, 24*time.Hour); !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tt.want)
		}
	}
}

func TestSignatureFieldIsSigned(t *testing.T) {
	// Newer clients add a signature field; it is part of what the hash covers.
	v, _ := url.ParseQuery(Sign(anna, now, token))
	v.Del("hash")
	v.Set("signature", "c2lnbmF0dXJl")
	v.Set("hash", hexSign(v))
	if _, err := Parse(v.Encode(), token, now, time.Hour); err != nil {
		t.Errorf("with signature: %v", err)
	}
}

// knownHash was computed independently (Python hmac) for the raw data of
// TestKnownVector with the test token.
const knownHash = "30fcdd7963d19f7952b70804c0075af3b2249bd32d67f6a3ac76f145cb24b6a6"

func hexSign(v url.Values) string {
	return hex.EncodeToString(sign(v, token, "hash"))
}
