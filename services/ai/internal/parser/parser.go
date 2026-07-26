// Package parser calls Claude to extract structured task fields from free text.
package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const systemPrompt = `You are a task parser. Extract structured fields from natural language task descriptions.

Always call the parse_task tool. Follow these rules:

TITLE: Extract a concise, action-oriented task title.

DESCRIPTION: Always generate a brief 1–2 sentence description that elaborates on what needs
to be done. Even if the user didn't provide extra context, infer what this task likely involves
based on the title and any implicit context. Keep it practical and specific.

PRIORITY MAPPING:
- "urgent", "critical", "ASAP", "immediately"  → urgent
- "high", "important", "soon"                   → high
- "medium", "normal", "moderate"                → medium
- "low", "minor", "someday", "eventually"       → low
- nothing clear                                  → omit (unspecified)

DATE PARSING: Use today's date for relative expressions ("next Friday", "tomorrow", "in 2 weeks").
Return dates as ISO 8601: "YYYY-MM-DDTHH:MM:SSZ". Use midnight UTC if no time is specified.

LIST MATCHING: If a project/list is mentioned, return its name exactly as given in available_lists
(case-insensitive match). If no lists are provided or no match found, return the raw name mentioned.

SUBTASK DECOMPOSITION: If a task clearly involves 2–5 distinct, concrete steps, list them as short
action-oriented subtask titles in the subtasks field. For simple single-action tasks, omit subtasks.`

// ParsedTask holds the structured output from the Claude parse_task tool.
type ParsedTask struct {
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Priority    string   `json:"priority,omitempty"` // "low"|"medium"|"high"|"urgent"
	DueAt       string   `json:"due_at,omitempty"`   // ISO 8601 or empty
	ListName    string   `json:"list_name,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Flagged     bool     `json:"flagged,omitempty"`
	Subtasks    []string `json:"subtasks,omitempty"`
}

// PriorityInt converts the string priority to the proto int32 representation.
func (p *ParsedTask) PriorityInt() int32 {
	switch strings.ToLower(p.Priority) {
	case "low":    return 1
	case "medium": return 2
	case "high":   return 3
	case "urgent": return 4
	default:       return 0
	}
}

// DueAtTime parses DueAt into a time.Time. Returns nil if not set or unparseable.
func (p *ParsedTask) DueAtTime() *time.Time {
	if p.DueAt == "" {
		return nil
	}
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, p.DueAt); err == nil {
			return &t
		}
	}
	return nil
}

// Parser wraps the Anthropic client for task-parsing calls.
type Parser struct {
	client *anthropic.Client
}

// New creates a Parser. Uses ANTHROPIC_API_KEY env var if apiKey is empty.
func New(apiKey string) *Parser {
	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	c := anthropic.NewClient(opts...)
	return &Parser{client: &c}
}

// languageDirectives steers Claude's output language via the user message.
// The system prompt is never modified so the cache prefix stays stable.
var languageDirectives = map[string]string{
	"en": "Respond in English.",
	"zh": "Respond in Simplified Chinese (简体中文). Title, description, and all text fields must be in Simplified Chinese.",
}

func languageDirective(lang string) string {
	if d, ok := languageDirectives[lang]; ok {
		return d
	}
	return languageDirectives["en"]
}

// Parse calls Claude to extract structured task fields from free text.
// lang steers output language without touching the cached system-prompt block.
func (p *Parser) Parse(ctx context.Context, text, today, lang string, listNames []string) (*ParsedTask, error) {
	userMsg := fmt.Sprintf("today: %s\navailable_lists: %s\n\nTask description:\n%s\n\n%s",
		today,
		strings.Join(listNames, ", "),
		text,
		languageDirective(lang),
	)

	resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_6,
		MaxTokens: 512,
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Tools: []anthropic.ToolUnionParam{
			{
				OfTool: &anthropic.ToolParam{
					Name:        "parse_task",
					Description: anthropic.String("Extract structured task fields from the natural language description."),
					InputSchema: anthropic.ToolInputSchemaParam{
						Properties: map[string]any{
							"title": map[string]any{
								"type":        "string",
								"description": "Concise task title (required).",
							},
							"description": map[string]any{
								"type":        "string",
								"description": "A brief 1-2 sentence description of what needs to be done. Always generate this even if not explicitly stated.",
							},
							"priority": map[string]any{
								"type":        "string",
								"enum":        []string{"low", "medium", "high", "urgent"},
								"description": "Task priority level.",
							},
							"due_at": map[string]any{
								"type":        "string",
								"description": "ISO 8601 due date/time, e.g. 2026-05-23T00:00:00Z",
							},
							"list_name": map[string]any{
								"type":        "string",
								"description": "Name of the list/project this task belongs to.",
							},
							"tags": map[string]any{
								"type":  "array",
								"items": map[string]any{"type": "string"},
								"description": "Relevant tags extracted from the description.",
							},
							"flagged": map[string]any{
								"type":        "boolean",
								"description": "True if the task is flagged as urgent or critical.",
							},
							"subtasks": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "2–5 short action-oriented subtask titles if the task has distinct concrete steps. Omit for simple tasks.",
							},
						},
						Required: []string{"title", "description"},
					},
				},
			},
		},
		ToolChoice: anthropic.ToolChoiceParamOfTool("parse_task"),
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userMsg)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("claude api: %w", err)
	}

	for _, block := range resp.Content {
		tu := block.AsToolUse()
		if tu.Name != "parse_task" {
			continue
		}
		var result ParsedTask
		if err := json.Unmarshal(tu.Input, &result); err != nil {
			return nil, fmt.Errorf("parse tool input: %w", err)
		}
		return &result, nil
	}

	return nil, fmt.Errorf("claude did not call parse_task (stop_reason=%s)", resp.StopReason)
}
