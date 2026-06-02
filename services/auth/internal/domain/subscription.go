package domain

import "time"

type Subscription struct {
	ID        string
	UserID    string
	Plan      string     // "pro"
	Source    string     // "promo_code" | "stripe"
	SourceRef string     // promo_code ID or Stripe subscription ID
	Status    string     // "active" | "cancelled"
	StartedAt time.Time
	ExpiresAt *time.Time // nil = lifetime
	CreatedAt time.Time
	UpdatedAt time.Time
}

type PromoCode struct {
	ID              string
	Code            string
	AllowedEmail    *string // nil = any user
	MaxUsageCount   *int    // nil = unlimited global uses
	MaxUsagePerUser int
	Disabled        bool
	Notes           string
	CreatedAt       time.Time
}
