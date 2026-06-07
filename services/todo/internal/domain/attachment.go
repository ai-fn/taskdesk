package domain

import "time"

const (
	AttachmentPending   = "pending"
	AttachmentCommitted = "committed"
	ScanClean           = "clean"
)

type Attachment struct {
	ID          string
	UserID      string
	TaskID      string
	Filename    string
	ContentType string
	SizeBytes   int64
	ObjectKey   string
	UploadID    string    // empty for single-PUT uploads
	ScanStatus  string
	Status      string
	CreatedAt   time.Time
}
