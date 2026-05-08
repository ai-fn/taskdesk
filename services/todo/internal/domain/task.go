package domain

import "time"

type Priority int32

const (
	PriorityUnspecified Priority = 0
	PriorityLow         Priority = 1
	PriorityMedium      Priority = 2
	PriorityHigh        Priority = 3
	PriorityUrgent      Priority = 4
)

type Status int32

const (
	StatusUnspecified Status = 0
	StatusTodo        Status = 1
	StatusInProgress  Status = 2
	StatusDone        Status = 3
)

type Task struct {
	ID          string
	UserID      string
	Title       string
	Description string
	Priority    Priority
	Status      Status
	Tags        []string
	DueAt       *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
