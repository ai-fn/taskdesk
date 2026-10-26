package parser

import (
	"strings"
	"testing"
	"time"

	"github.com/fsociety/todo/pkg/tz"
)

func TestDueAtTime(t *testing.T) {
	sh := tz.Load("Asia/Shanghai")
	for _, tc := range []struct {
		name, in, want string // want is the UTC instant, "" = nil
	}{
		{"empty", "", ""},
		{"garbage", "next friday", ""},
		{"offset wins over loc", "2026-05-20T17:00:00+08:00", "2026-05-20T09:00:00Z"},
		{"zulu", "2026-05-20T09:00:00Z", "2026-05-20T09:00:00Z"},
		{"date only is local midnight", "2026-05-20", "2026-05-19T16:00:00Z"},
		{"no offset is local", "2026-05-20T17:00:00", "2026-05-20T09:00:00Z"},
	} {
		got := (&ParsedTask{DueAt: tc.in}).DueAtTime(sh)
		switch {
		case tc.want == "" && got != nil:
			t.Errorf("%s: got %v, want nil", tc.name, got)
		case tc.want != "" && (got == nil || got.UTC().Format(time.RFC3339) != tc.want):
			t.Errorf("%s: got %v, want %s", tc.name, got, tc.want)
		}
	}
}

func TestUserMessageCarriesTimeZone(t *testing.T) {
	msg := userMessage("buy milk tomorrow", "2026-05-19", "Asia/Shanghai", "en", []string{"Home"})
	for _, want := range []string{"today: 2026-05-19", "timezone: Asia/Shanghai", "available_lists: Home", "buy milk tomorrow"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestDueAtTime_AppliesDSTOfTheLocalDate(t *testing.T) {
	ny := tz.Load("America/New_York")
	for in, want := range map[string]string{
		"2026-10-30T17:00:00": "2026-10-30T21:00:00Z", // EDT, UTC-4
		"2026-11-02T17:00:00": "2026-11-02T22:00:00Z", // EST after the 1 Nov change, UTC-5
	} {
		got := (&ParsedTask{DueAt: in}).DueAtTime(ny)
		if got == nil || got.UTC().Format(time.RFC3339) != want {
			t.Errorf("%s: got %v, want %s", in, got, want)
		}
	}
}
