package domain

import "time"

type User struct {
	ID            string
	Email         string
	Name          string
	PasswordHash  *string // nil for OAuth-only users
	Language      string
	EmailVerified bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
