package domain

import "time"

type User struct {
	ID           string
	Email        string
	Name         string
	PasswordHash *string // nil for OAuth-only users
	Language     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
