// Package events defines the domain event schemas published to Kafka.
package events

import "time"

// Topic is the Kafka topic for all task events.
const Topic = "task.events.v1"

// AuthEmailTopic is the Kafka topic for transactional auth emails.
const AuthEmailTopic = "auth.email.v1"

// Task event types.
const (
	TypeTaskCreated   = "task.created"
	TypeTaskUpdated   = "task.updated"
	TypeTaskCompleted = "task.completed"
	TypeTaskDeleted   = "task.deleted"
)

// Auth email purposes.
const (
	PurposePasswordReset = "password_reset"
	PurposeEmailVerify   = "email_verify"
)

// AuthEmailEvent is published by auth-service to trigger transactional emails.
// The raw OTP code is included in the payload so notification-service can
// embed it in the email body; it is never persisted to the auth DB at rest.
type AuthEmailEvent struct {
	EventID     string    `json:"event_id"`
	UserID      string    `json:"user_id"`
	Email       string    `json:"email"`
	Purpose     string    `json:"purpose"` // PurposePasswordReset | PurposeEmailVerify
	Code        string    `json:"code"`    // raw OTP — embed in email
	Language    string    `json:"language"`
	ExpiresMins int       `json:"expires_mins"` // for display in email body
	OccurredAt  time.Time `json:"occurred_at"`
}

// TaskEvent is the JSON payload written to todo.outbox and published to Kafka.
// Key is always the user_id so events for one user land on the same partition.
// Language is denormalized from the user's preference at production time so that
// notification-service can localize emails without a synchronous auth lookup.
type TaskEvent struct {
	EventID    string    `json:"event_id"`
	EventType  string    `json:"event_type"`
	UserID     string    `json:"user_id"`
	TaskID     string    `json:"task_id"`
	TaskTitle  string    `json:"task_title"`
	OccurredAt time.Time `json:"occurred_at"`
	Language   string    `json:"language"`
}
