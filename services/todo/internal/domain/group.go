package domain

import "time"

type ListGroup struct {
	ID        string
	UserID    string
	Name      string
	Position  int
	CreatedAt time.Time
	UpdatedAt time.Time
}
