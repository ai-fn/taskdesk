package tz

import "testing"

func TestBrowserEmittedNamesAreValid(t *testing.T) {
	for _, n := range []string{"Asia/Calcutta", "Asia/Saigon", "America/Argentina/Buenos_Aires", "Europe/Kiev", "Pacific/Auckland", "Asia/Kolkata"} {
		if !Valid(n) {
			t.Errorf("Valid(%q) = false", n)
		}
	}
}
