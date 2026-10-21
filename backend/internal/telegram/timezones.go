package telegram

import (
	"fmt"
	"time"
)

// timezone is a choice in /settings: Russian timezones, west to east.
type timezone struct {
	city string
	name string // IANA
}

var timezones = []timezone{
	{"Калининград", "Europe/Kaliningrad"},
	{"Москва", "Europe/Moscow"},
	{"Самара", "Europe/Samara"},
	{"Екатеринбург", "Asia/Yekaterinburg"},
	{"Омск", "Asia/Omsk"},
	{"Новосибирск", "Asia/Novosibirsk"},
	{"Красноярск", "Asia/Krasnoyarsk"},
	{"Иркутск", "Asia/Irkutsk"},
	{"Якутск", "Asia/Yakutsk"},
	{"Владивосток", "Asia/Vladivostok"},
	{"Магадан", "Asia/Magadan"},
	{"Камчатка", "Asia/Kamchatka"},
}

// label is "Москва, UTC+3"; the offset is taken from the tz database, not hardcoded.
func (tz timezone) label() string {
	loc, err := time.LoadLocation(tz.name)
	if err != nil {
		return tz.city
	}
	return tz.city + ", " + utcOffset(loc)
}

// timezoneName shows a location the way the settings list does.
func timezoneName(loc *time.Location) string {
	for _, tz := range timezones {
		if tz.name == loc.String() {
			return tz.label()
		}
	}
	return loc.String() + ", " + utcOffset(loc)
}

func utcOffset(loc *time.Location) string {
	_, offset := time.Now().In(loc).Zone()
	if offset%3600 == 0 {
		return fmt.Sprintf("UTC%+d", offset/3600)
	}
	return fmt.Sprintf("UTC%+d:%02d", offset/3600, abs(offset%3600)/60)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
