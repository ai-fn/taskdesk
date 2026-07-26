// Package planner calls Claude to generate a daily task prioritization plan.
package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// systemPrompt is stable across all users — cached by Claude on the first call.
const systemPrompt = `You are a daily task prioritization assistant. Given a user's tasks, produce a focused, human-readable plan for today that helps them understand why each task matters.

PROCESS:
1. First, write 1-2 sentences (≤25 words) describing today's single focus theme. Be direct and motivating. E.g. "Today is about shipping the auth fix and clearing two overdue items."
2. Then immediately call submit_daily_plan with your structured plan.

OUTPUT RULES — follow exactly:
- summary: 1-2 sentences (≤25 words). Clear, motivating theme. E.g. "Ship auth fix, then clear urgent overdue items" or "Handle production issues, prep sprint planning."

- items: Select 5-7 most important tasks. Prioritize in this order:
  1. Past-due items (oldest first — most time-sensitive)
  2. Tasks due today (by priority)
  3. In-progress work (maintain momentum, unblock others)
  4. High-priority upcoming (next 1-2 items for forward visibility)
  Skip routine/low-impact items if total exceeds 7.

- rationale: 10-15 words per task. Human-readable. Show *why* it matters today.
  Good examples:
    • "Overdue 5 days, affects team capacity" (when + impact)
    • "Due today, high priority, unblocks feature launch" (deadline + consequence)
    • "Production bug, user-facing, needs immediate fix" (severity + scope)
    • "In progress, one hour left, finish today" (status + estimate)
    • "Prep for tomorrow meeting, high visibility" (forward-looking reason)
  Bad: "urgent", "do it", "important" — be specific about *why*.

RANKING ORDER (apply within each group above):
1. Past-due items (oldest first)
2. Tasks due today (highest priority first)
3. In-progress work (highest priority first)
4. Upcoming high-priority (nearest deadline first)

You MUST write plain text and then - call submit_daily_plan. Do not write plain text after the tool call.`

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
	UserID            string      `json:"user_id"`
	Today             string      `json:"today"`
	OpenTasks         []TaskInput `json:"open_tasks"`
	RecentlyCompleted []TaskInput `json:"recently_completed"`
}

// languageDirectives contains per-language instructions injected into the USER
// message (not the cached system prompt) to steer Claude's output language.
// Placing the directive here preserves the stable system-prompt cache prefix.
var languageDirectives = map[string]string{
	"en": "Respond in English.",
	"zh": "Respond in Simplified Chinese (简体中文). All summary and rationale text must be in Simplified Chinese.",
}

func languageDirective(lang string) string {
	if d, ok := languageDirectives[lang]; ok {
		return d
	}
	return languageDirectives["en"]
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
// lang is normalized (e.g. "en", "zh"); it steers the output language without
// touching the cached system prompt.
func (p *Planner) Generate(ctx context.Context, uc UserContext, lang string) (*SubmitDailyPlanInput, error) {
	userJSON, err := json.Marshal(uc)
	if err != nil {
		return nil, fmt.Errorf("marshal user context: %w", err)
	}
	// Append language directive at end of user message for recency — this ensures
	// the cached systemPrompt block stays byte-for-byte stable across locales.
	userText := string(userJSON) + "\n\n" + languageDirective(lang)

	resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_6,
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
				anthropic.NewTextBlock(userText),
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

// GenerateStream calls Claude with streaming enabled. Text deltas are forwarded
// to onText as they arrive; the final parsed plan is returned when the stream ends.
// ToolChoice is auto so Claude can write visible narrative text before calling the tool.
// lang steers output language without touching the cached system-prompt block.
func (p *Planner) GenerateStream(ctx context.Context, uc UserContext, lang string, onText func(string)) (*SubmitDailyPlanInput, error) {
	userJSON, err := json.Marshal(uc)
	if err != nil {
		return nil, fmt.Errorf("marshal user context: %w", err)
	}
	userText := string(userJSON) + "\n\n" + languageDirective(lang)

	stream := p.client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_6,
		MaxTokens: 1024,
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
		// auto — Claude writes narrative text first, then calls the tool.
		ToolChoice: anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(
				anthropic.NewTextBlock(userText),
			),
		},
	})
	defer func() { _ = stream.Close() }()

	var acc anthropic.Message
	var narrative strings.Builder
	for stream.Next() {
		event := stream.Current()

		// Forward text deltas to caller for live display and accumulate locally.
		if event.Type == "content_block_delta" {
			e := event.AsContentBlockDelta()
			if td, ok := e.Delta.AsAny().(anthropic.TextDelta); ok && td.Text != "" {
				onText(td.Text)
				narrative.WriteString(td.Text)
			}
		}

		if err := acc.Accumulate(event); err != nil {
			return nil, fmt.Errorf("accumulate stream event: %w", err)
		}
	}
	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("claude stream: %w", err)
	}

	// Extract submit_daily_plan tool use from accumulated message.
	for _, block := range acc.Content {
		tu := block.AsToolUse()
		if tu.Name != "submit_daily_plan" {
			continue
		}
		var plan SubmitDailyPlanInput
		if err := json.Unmarshal(tu.Input, &plan); err != nil {
			return nil, fmt.Errorf("parse tool input: %w", err)
		}
		// Replace the tool-call summary with the streamed narrative — the user
		// sees this text on screen, so what's stored in DB should match exactly.
		if s := strings.TrimSpace(narrative.String()); s != "" {
			plan.Summary = s
		}
		return &plan, nil
	}

	return nil, fmt.Errorf("claude did not call submit_daily_plan (stop_reason=%s)", acc.StopReason)
}

// CacheStats holds prompt cache token counters from a response for observability.
type CacheStats struct {
	CacheCreationTokens int64
	CacheReadTokens     int64
	InputTokens         int64
	OutputTokens        int64
	GeneratedAt         time.Time
}
