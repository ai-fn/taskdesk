// Package scorer calls Claude to estimate the probability (0–100) that a user
// will complete a given task, based on the task's attributes and the user's
// recent completion history. This is the async enrichment pattern: todo-service
// writes the task first, publishes an event, and we enrich it here.
package scorer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const systemPrompt = `You are a productivity analyst. Given a task and a user's recent completion history, estimate the probability (0–100) that the user will actually complete this task.

Be realistic and data-driven. Base your score primarily on:
1. The user's completion rate for similar tasks (title keywords, priority, list)
2. How far the due date is — more time = higher probability
3. Task priority — urgent/high tasks are completed more often than low ones
4. Current workload — many open tasks can signal overload

Return your estimate as a score 0–100 and a single insight sentence (≤20 words) that explains the main factor driving your estimate. Be specific and human.

Examples of good insights:
- "You complete high-priority tasks 80% of the time; tight 2-day deadline helps focus."
- "Similar 'prepare' tasks rarely get done — only 30% completion in past 30 days."
- "No due date and low priority — historically these get postponed indefinitely."
- "You're currently juggling 15 open tasks; capacity may be the limiting factor."

Always call score_task.`

// ScoreInput describes the task being scored.
type ScoreInput struct {
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Priority    string    `json:"priority"`
	DueAt       *string   `json:"due_at,omitempty"` // ISO 8601 or nil
	Tags        []string  `json:"tags,omitempty"`
	ListName    string    `json:"list_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// HistoryTask is one entry in the user's recent task history.
type HistoryTask struct {
	Title       string `json:"title"`
	Priority    string `json:"priority"`
	Status      string `json:"status"` // "done" or "open"
	DaysElapsed int    `json:"days_elapsed"` // days since creation
}

// ScoreResult is the structured output from the score_task tool.
type ScoreResult struct {
	Score   int    `json:"score"`   // 0–100
	Insight string `json:"insight"` // ≤20 words
}

// Scorer wraps the Anthropic client for task-scoring calls.
type Scorer struct {
	client *anthropic.Client
}

// New creates a Scorer. Uses ANTHROPIC_API_KEY env var if apiKey is empty.
func New(apiKey string) *Scorer {
	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	c := anthropic.NewClient(opts...)
	return &Scorer{client: &c}
}

// Score calls Claude and returns a completion probability score (0–100) plus
// a short insight explaining the key factor driving the estimate.
func (sc *Scorer) Score(ctx context.Context, today string, task ScoreInput, history []HistoryTask) (*ScoreResult, error) {
	type userMsg struct {
		Today        string        `json:"today"`
		Task         ScoreInput    `json:"task"`
		RecentTasks  []HistoryTask `json:"recent_tasks_30d"` // up to 50 entries
		OpenTaskCount int          `json:"open_task_count"`
	}

	// Count open tasks from history for workload context.
	open := 0
	for _, h := range history {
		if h.Status == "open" {
			open++
		}
	}

	msg := userMsg{
		Today:         today,
		Task:          task,
		RecentTasks:   history,
		OpenTaskCount: open,
	}
	msgJSON, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal input: %w", err)
	}

	resp, err := sc.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_6,
		MaxTokens: 256,
		System: []anthropic.TextBlockParam{
			{
				Text:         systemPrompt,
				CacheControl: anthropic.CacheControlEphemeralParam{},
			},
		},
		Tools: []anthropic.ToolUnionParam{
			{
				OfTool: &anthropic.ToolParam{
					Name:        "score_task",
					Description: anthropic.String("Return the completion probability score and a short insight."),
					InputSchema: anthropic.ToolInputSchemaParam{
						Properties: map[string]any{
							"score": map[string]any{
								"type":        "integer",
								"minimum":     0,
								"maximum":     100,
								"description": "Estimated probability (0–100) the user will complete this task.",
							},
							"insight": map[string]any{
								"type":        "string",
								"description": "One sentence (≤20 words) explaining the main factor driving the score.",
							},
						},
						Required: []string{"score", "insight"},
					},
				},
			},
		},
		ToolChoice: anthropic.ToolChoiceParamOfTool("score_task"),
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(string(msgJSON))),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("claude api: %w", err)
	}

	for _, block := range resp.Content {
		tu := block.AsToolUse()
		if tu.Name != "score_task" {
			continue
		}
		var result ScoreResult
		if err := json.Unmarshal(tu.Input, &result); err != nil {
			return nil, fmt.Errorf("parse tool input: %w", err)
		}
		// Clamp to valid range.
		if result.Score < 0 {
			result.Score = 0
		}
		if result.Score > 100 {
			result.Score = 100
		}
		result.Insight = strings.TrimSpace(result.Insight)
		return &result, nil
	}

	return nil, fmt.Errorf("claude did not call score_task (stop_reason=%s)", resp.StopReason)
}
