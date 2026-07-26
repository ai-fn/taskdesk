package domain

import "time"

type VerificationCode struct {
	ID         string
	UserID     string
	Purpose    string
	CodeHash   string
	Attempts   int16
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	CreatedAt  time.Time
}
