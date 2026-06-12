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
	ListID            *string    // nil = no list
	Flagged           bool
	DeletedAt         *time.Time // nil = not soft-deleted
	CompletionScore   *int       // nil = not yet scored; 0–100 when present
	CompletionInsight string
	ParentTaskID      *string    // nil = root task
	SubtaskCount      int        // computed by store
	SubtaskDoneCount  int        // computed by store
	Blocked           bool       // computed by store: any unfinished dep
	DependsOnTaskIDs  []string   // computed by store: aggregated dep IDs
	AttachmentCount   int32      // denormalized from tasks.attachment_count
}
