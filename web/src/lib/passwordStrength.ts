// Password-strength scoring, mirrored from the Go policy in
// services/auth/internal/password/password.go. Keep the two in sync so the
// strength bar a user sees matches exactly what the server will accept.

/** Minimum acceptable strength score. Anything below is "weak" and rejected. */
export const MIN_PASSWORD_SCORE = 2

export type StrengthLevel = 'weak' | 'fair' | 'good' | 'strong'

/**
 * Rates a password from 0 (weakest) to 4 (strongest):
 *   +1 length >= 8
 *   +1 length >= 12
 *   +1 has both a lowercase and an uppercase letter
 *   +1 has a digit
 *   +1 has a symbol (non-alphanumeric)
 * capped at 4.
 */
export function scorePassword(pw: string): number {
  if (!pw) return 0
  const chars = [...pw] // count code points, matching Go's rune count
  const length = chars.length

  const hasLower = /\p{Ll}/u.test(pw)
  const hasUpper = /\p{Lu}/u.test(pw)
  const hasDigit = /\d/.test(pw)
  const hasSymbol = /[^\p{L}\p{N}]/u.test(pw)

  let score = 0
  if (length >= 8) score++
  if (length >= 12) score++
  if (hasLower && hasUpper) score++
  if (hasDigit) score++
  if (hasSymbol) score++
  return Math.min(score, 4)
}

export function scoreToLevel(score: number): StrengthLevel {
  if (score <= 1) return 'weak'
  if (score === 2) return 'fair'
  if (score === 3) return 'good'
  return 'strong'
}
