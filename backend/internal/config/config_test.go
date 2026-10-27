package config

import "testing"

func TestTelegramProxyURL(t *testing.T) {
	for proxy, ok := range map[string]bool{
		"":                                true,
		"socks5://telegram-proxy:1080":    true,
		"socks5://user:pass@1.2.3.4:1080": true,
		"http://proxy.example.com:3128":   true,
		"ftp://proxy:21":                  false,
		"telegram-proxy:1080":             false,
		"socks5://":                       false,
	} {
		u, err := (Bot{TelegramProxy: proxy}).TelegramProxyURL()
		if (err == nil) != ok || (proxy == "" && u != nil) {
			t.Errorf("TELEGRAM_PROXY=%q: %v, %v", proxy, u, err)
		}
	}
}

func TestDevUserOnlyInDev(t *testing.T) {
	for env, want := range map[string]int64{"dev": 42, "prod": 0, "": 0, "production": 0, "DEV": 0} {
		if got := (API{Env: env, AuthDevUserID: 42}).DevUser(); got != want {
			t.Errorf("APP_ENV=%q: dev user = %d, want %d", env, got, want)
		}
	}
}
