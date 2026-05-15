// Package store handles persistence of daily plans for the ai-service.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DailyPlan is the stored representation of a Claude-generated plan.
type DailyPlan struct {
	PlanID      string     `db:"plan_id"`
	UserID      string     `db:"user_id"`
	Summary     string     `db:"summary"`
	ItemsJSON   []byte     `db:"items_json"`
	GeneratedAt time.Time  `db:"generated_at"`
}

// PlanItem mirrors planner.PlanItem for DB serialization.
type PlanItem struct {
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	Rank      int    `json:"rank"`
	Rationale string `json:"rationale"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// SavePlan persists a generated plan. Replaces any existing plan for the same user+day.
func (s *Store) SavePlan(ctx context.Context, planID, userID, summary string, items []PlanItem, generatedAt time.Time) error {
	itemsJSON, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("marshal items: %w", err)
	}
	planDate := generatedAt.UTC().Format("2006-01-02")
	_, err = s.db.Exec(ctx, `
		INSERT INTO ai.daily_plans (plan_id, user_id, plan_date, summary, items_json, generated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, plan_date)
		DO UPDATE SET plan_id = EXCLUDED.plan_id, summary = EXCLUDED.summary,
		              items_json = EXCLUDED.items_json, generated_at = EXCLUDED.generated_at`,
		planID, userID, planDate, summary, itemsJSON, generatedAt,
	)
	return err
}

// GetLatestPlan returns the most recent plan for a user, or nil if none exists.
func (s *Store) GetLatestPlan(ctx context.Context, userID string) (*DailyPlan, error) {
	var p DailyPlan
	err := s.db.QueryRow(ctx, `
		SELECT plan_id, user_id, summary, items_json, generated_at
		FROM ai.daily_plans
		WHERE user_id = $1
		ORDER BY generated_at DESC
		LIMIT 1`, userID,
	).Scan(&p.PlanID, &p.UserID, &p.Summary, &p.ItemsJSON, &p.GeneratedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get latest plan: %w", err)
	}
	return &p, nil
}
