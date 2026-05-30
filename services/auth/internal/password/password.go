// Package password holds the shared password-strength policy for auth-service.
//
// The scoring rules here are intentionally mirrored by the frontend
// (web/src/lib/passwordStrength.ts) so the strength bar a user sees matches
// exactly what the server will accept.
package password

import (
	"errors"
	"unicode"
)

// MinLength is the minimum acceptable password length.
const MinLength = 8

// MinScore is the minimum acceptable strength score (see Score). Anything below
// this is considered "weak" and rejected.
const MinScore = 2

// Sentinel validation errors. Messages are surfaced verbatim to the client via
// the gateway's gRPC error extraction, so keep them user-readable.
var (
	ErrTooShort = errors.New("password must be at least 8 characters")
	ErrTooWeak  = errors.New("password is too weak; add length or a mix of letters, numbers and symbols")
)

// Score rates a password from 0 (weakest) to 4 (strongest):
//
//	+1 length >= 8
//	+1 length >= 12
//	+1 has both a lowercase and an uppercase letter
//	+1 has a digit
//	+1 has a symbol (non-alphanumeric)
//
// capped at 4. Labels: 0–1 weak, 2 fair, 3 good, 4 strong.
func Score(pw string) int {
	var hasLower, hasUpper, hasDigit, hasSymbol bool
	for _, r := range pw {
		switch {
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsDigit(r):
			hasDigit = true
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			hasSymbol = true
		}
	}

	// Count runes, not bytes, so multibyte characters aren't over-counted.
	length := len([]rune(pw))

	score := 0
	if length >= 8 {
		score++
	}
	if length >= 12 {
		score++
	}
	if hasLower && hasUpper {
		score++
	}
	if hasDigit {
		score++
	}
	if hasSymbol {
		score++
	}
	if score > 4 {
		score = 4
	}
	return score
}

// Validate returns nil if pw satisfies the strength policy, or a sentinel error
// describing why it was rejected.
func Validate(pw string) error {
	if len([]rune(pw)) < MinLength {
		return ErrTooShort
	}
	if Score(pw) < MinScore {
		return ErrTooWeak
	}
	return nil
}
