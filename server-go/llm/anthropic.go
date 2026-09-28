package llm

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// BookModel is what every role runs on, grading, planning and authoring
// alike, through the Claude Code CLI or the API, unless the config names
// another.
const BookModel = "claude-fable-5-1"

// DefaultAnthropicModel is what the server runs on when it talks to the API
// directly rather than through the Claude Code CLI.
const DefaultAnthropicModel = BookModel

// DefaultVisionModel reads handwriting when the server talks to the API.
const DefaultVisionModel = BookModel

// Anthropic is the direct-API backend, used when the server runs somewhere with
// a key rather than a Claude Code login.
type Anthropic struct {
	Model  string
	client *anthropic.Client
	err    string
}

type roleLimit struct {
	maxTokens int64
	effort    anthropic.OutputConfigEffort
}

// max_tokens caps thinking and response text together, so these sit well above
// the JSON payloads themselves.
var anthropicLimits = map[string]roleLimit{
	"grader":  {4000, anthropic.OutputConfigEffortMedium},
	"planner": {6000, anthropic.OutputConfigEffortMedium},
	"author":  {16000, anthropic.OutputConfigEffortHigh},
}

func NewAnthropic(model string) *Anthropic {
	a := &Anthropic{Model: model}
	if a.Model == "" {
		a.Model = DefaultAnthropicModel
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("ANTHROPIC_AUTH_TOKEN") == "" {
		a.err = "no credentials: set ANTHROPIC_API_KEY"
		return a
	}
	c := anthropic.NewClient(option.WithMaxRetries(2))
	a.client = &c
	return a
}

func (a *Anthropic) Status() Status {
	return Status{
		Connected: a.client != nil,
		Upstream:  "anthropic",
		Model:     a.Model,
		Error:     a.err,
	}
}

func (a *Anthropic) Structured(role, system, user string, schema map[string]any,
	schemaName string, out any) error {
	if a.client == nil {
		return Errorf("anthropic: %s", a.err)
	}
	limits, ok := anthropicLimits[role]
	if !ok {
		limits = anthropicLimits["planner"]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	msg, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.Model),
		MaxTokens: limits.maxTokens,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: limits.effort,
			Format: anthropic.JSONOutputFormatParam{Schema: strictSchema(schema)},
		},
	})
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return Errorf("anthropic: %s", apiErr.Error())
		}
		return Errorf("anthropic: %v", err)
	}
	if msg.StopReason == anthropic.StopReasonRefusal {
		return Errorf("anthropic: request refused by safety classifiers")
	}
	for _, block := range msg.Content {
		if text := block.Text; text != "" {
			return unmarshalLoose(text, out)
		}
	}
	return Errorf("anthropic: empty response")
}
