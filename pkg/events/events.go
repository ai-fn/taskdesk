// Package events defines the task domain event schema published to Kafka.
package events

import "time"

// Topic is the Kafka topic for all task events.
const Topic = "task.events.v1"

// Event types.
const (
	TypeTaskCreated   = "task.created"
	TypeTaskUpdated   = "task.updated"
	TypeTaskCompleted = "task.completed"
	TypeTaskDeleted   = "task.deleted"
)

// TaskEvent is the JSON payload written to todo.outbox and published to Kafka.
// Key is always the user_id so events for one user land on the same partition.
type TaskEvent struct {
	EventID    string    `json:"event_id"`
	EventType  string    `json:"event_type"`
	UserID     string    `json:"user_id"`
	TaskID     string    `json:"task_id"`
	TaskTitle  string    `json:"task_title"`
	OccurredAt time.Time `json:"occurred_at"`
}
