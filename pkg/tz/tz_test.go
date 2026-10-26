package tz

import "testing"

func TestNormalizeAndValid(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"Asia/Shanghai", "Asia/Shanghai", true},
		{" America/New_York ", "America/New_York", false}, // Valid is strict; Normalize trims
		{"UTC", "UTC", true},
		{"", "UTC", false},
		{"Local", "UTC", false},
		{"Mars/Olympus", "UTC", false},
		{"../etc/passwd", "UTC", false},
		{"Factory", "UTC", false},
		{"EST", "UTC", false},
		{"GMT", "GMT", true},
	} {
		if got := Valid(tc.in); got != tc.ok {
			t.Errorf("Valid(%q) = %v, want %v", tc.in, got, tc.ok)
		}
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLoadFallsBackToUTC(t *testing.T) {
	if Load("nope").String() != "UTC" {
		t.Fatal("expected UTC fallback")
	}
	if Load("Asia/Shanghai").String() != "Asia/Shanghai" {
		t.Fatal("expected Asia/Shanghai")
	}
}
