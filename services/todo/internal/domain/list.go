package domain

import "time"

type List struct {
	ID        string
	UserID    string
	Name      string
	Color     string
	Icon      string
	Position  int
	IsHidden  bool
	TaskCount int // computed by store query, not stored in DB
	GroupID   *string
	CreatedAt time.Time
	UpdatedAt time.Time
}
