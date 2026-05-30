package password

import (
	"errors"
	"testing"
)

func TestScore(t *testing.T) {
	tests := []struct {
		name string
		pw   string
		want int
	}{
		{"empty", "", 0},
		{"short lowercase only", "abc", 0},
		{"8 lowercase only", "abcdefgh", 1},          // length>=8
		{"8 with upper", "Abcdefgh", 2},              // length>=8, lower+upper
		{"8 with upper+digit", "Abcdefg1", 3},        // +digit
		{"8 with upper+digit+symbol", "Abcdef1!", 4}, // +symbol
		{"12 lowercase only", "abcdefghijkl", 2},     // length>=8, length>=12
		{"12 mixed maxes out", "Abcdefghij1!", 4},    // +lower/upper +digit +symbol, capped
		{"all classes capped at 4", "Abcdefghijkl1234!@#$", 4},
		{"digits only short", "1234567", 1}, // <8 but has a digit
		{"digits only 8", "12345678", 2},    // length>=8, digit
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Score(tt.pw); got != tt.want {
				t.Errorf("Score(%q) = %d, want %d", tt.pw, got, tt.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		pw      string
		wantErr error
	}{
		{"too short", "Ab1!", ErrTooShort},
		{"7 chars rejected", "Abc123!", ErrTooShort},
		{"8 lowercase only too weak", "abcdefgh", ErrTooWeak}, // score 1
		{"8 with upper ok", "Abcdefgh", nil},                  // score 2 (fair)
		{"8 digits ok", "12345678", nil},                      // score 2 (fair)
		{"strong ok", "Abcdef1!", nil},                        // score 4
		{"long lowercase ok", "abcdefghijkl", nil},            // score 2 via length
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.pw)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate(%q) = %v, want %v", tt.pw, err, tt.wantErr)
			}
		})
	}
}
