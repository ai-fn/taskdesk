package domain

import "time"

type User struct {
	ID               string
	Email            string
	Name             string
	PasswordHash     *string // nil for OAuth-only users
	Language         string
	TimeZone         string // IANA name
	EmailVerified    bool
	Plan             string  // "free" | "pro"
	StripeCustomerID *string // nil until Stripe checkout is initiated
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
