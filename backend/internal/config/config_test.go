package config

import "testing"

func TestDevUserOnlyInDev(t *testing.T) {
	for env, want := range map[string]int64{"dev": 42, "prod": 0, "": 0, "production": 0, "DEV": 0} {
		if got := (API{Env: env, AuthDevUserID: 42}).DevUser(); got != want {
			t.Errorf("APP_ENV=%q: dev user = %d, want %d", env, got, want)
		}
	}
}
