package domain

import "time"

// Session represents an active login session (one row in auth.sessions).
type Session struct {
	ID            string
	UserID        string
	TokenHash     string
	IPAddress     string
	UserAgent     string
	CreatedAt     time.Time
	LastActiveAt  time.Time
	ExpiresAt     time.Time
}
