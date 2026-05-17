// Package planner calls Claude to generate a daily task prioritization plan.
package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// systemPrompt is stable across all users — cached by Claude on the first call.
const systemPrompt = `You are a compact daily planner. Given a user's tasks, produce a focused plan for today.

OUTPUT RULES — follow exactly:
- summary: one short sentence (≤20 words). State the day's single theme, e.g. "Focus on urgent fixes and two key project milestones."
- items: include ONLY tasks due today, past-due, or the 3-5 highest-priority upcoming tasks. Skip low-value tasks if the list is long.
- rationale: ≤8 words per task. Be direct, e.g. "Due today", "Blocks team", "High priority, quick win".

RANKING ORDER:
1. Past-due tasks (oldest first)
2. Due today (by priority)
3. IN_PROGRESS tasks (keep momentum)
4. Upcoming HIGH/URGENT tasks nearest in time

You MUST call the submit_daily_plan tool. Do not reply in plain text.`

// SubmitDailyPlanInput matches the JSON schema of the submit_daily_plan tool.
type SubmitDailyPlanInput struct {
	Summary string     `json:"summary"`
	Items   []PlanItem `json:"items"`
}

// PlanItem is one task in the prioritized plan.
type PlanItem struct {
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	Rank      int    `json:"rank"`
	Rationale string `json:"rationale"`
}

// TaskInput is the task shape sent to Claude.
type TaskInput struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Priority    string   `json:"priority"`
	Status      string   `json:"status"`
	DueAt       *string  `json:"due_at,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// UserContext is the full context passed to Claude as the user message.
type UserContext struct {
	UserID           string      `json:"user_id"`
	Today            string      `json:"today"`
	OpenTasks        []TaskInput `json:"open_tasks"`
	RecentlyCompleted []TaskInput `json:"recently_completed"`
}

// Planner wraps the Anthropic client.
type Planner struct {
	client *anthropic.Client
}

// New creates a Planner. apiKey is read from ANTHROPIC_API_KEY env if empty.
func New(apiKey string) *Planner {
	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	c := anthropic.NewClient(opts...)
	return &Planner{client: &c}
}

// Generate calls Claude and returns a prioritized plan for the user.
func (p *Planner) Generate(ctx context.Context, uc UserContext) (*SubmitDailyPlanInput, error) {
	userJSON, err := json.Marshal(uc)
	if err != nil {
		return nil, fmt.Errorf("marshal user context: %w", err)
	}

	resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeOpus4_7,
		MaxTokens: 1024,
		// Stable system prompt — Claude caches this prefix, saving ~$0.30 per call.
		System: []anthropic.TextBlockParam{
			{
				Text:         systemPrompt,
				CacheControl: anthropic.CacheControlEphemeralParam{},
			},
		},
		Tools: []anthropic.ToolUnionParam{
			{
				OfTool: &anthropic.ToolParam{
					Name:        "submit_daily_plan",
					Description: anthropic.String("Submit the structured daily task prioritization plan."),
					InputSchema: anthropic.ToolInputSchemaParam{
						Properties: map[string]any{
							"summary": map[string]any{
								"type":        "string",
								"description": "One sentence (≤20 words) stating the day's single focus theme.",
							},
							"items": map[string]any{
								"type": "array",
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"task_id":    map[string]any{"type": "string"},
										"task_title": map[string]any{"type": "string"},
										"rank":       map[string]any{"type": "integer", "description": "1 = highest priority"},
										"rationale":  map[string]any{"type": "string", "description": "≤8 words: why this rank."},
									},
									"required": []string{"task_id", "task_title", "rank", "rationale"},
								},
								"description": "Today's and nearest high-priority tasks in ranked order (rank 1 first). Omit low-value tasks.",
							},
						},
						Required: []string{"summary", "items"},
					},
				},
			},
		},
		// Force the model to call submit_daily_plan — no free-text fallback.
		ToolChoice: anthropic.ToolChoiceParamOfTool("submit_daily_plan"),
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(
				anthropic.NewTextBlock(string(userJSON)),
			),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("claude api: %w", err)
	}

	// Extract the tool use block.
	for _, block := range resp.Content {
		tu := block.AsToolUse()
		if tu.Name != "submit_daily_plan" {
			continue
		}
		var plan SubmitDailyPlanInput
		if err := json.Unmarshal(tu.Input, &plan); err != nil {
			return nil, fmt.Errorf("parse tool input: %w", err)
		}
		return &plan, nil
	}

	return nil, fmt.Errorf("claude did not call submit_daily_plan (stop_reason=%s)", resp.StopReason)
}

// CacheStats holds prompt cache token counters from a response for observability.
type CacheStats struct {
	CacheCreationTokens int64
	CacheReadTokens     int64
	InputTokens         int64
	OutputTokens        int64
	GeneratedAt         time.Time
}
