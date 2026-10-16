// Package agent runs the Messages API tool-use loop against an MCP server:
// the model sees the server's tools and instructions as served, every call is
// forwarded, and tokens and tool calls are counted.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nodelogicau/particulars-bench/internal/impl"
)

// Model sends one Messages API request.
type Model interface {
	New(ctx context.Context, p anthropic.MessageNewParams) (*anthropic.Message, error)
}

// Anthropic is the Model backed by the Anthropic API (ANTHROPIC_API_KEY).
type Anthropic struct{ client anthropic.Client }

// NewAnthropic returns a client with the SDK's default retries.
func NewAnthropic(opts ...option.RequestOption) *Anthropic {
	return &Anthropic{client: anthropic.NewClient(opts...)}
}

// New implements Model.
func (a *Anthropic) New(ctx context.Context, p anthropic.MessageNewParams) (*anthropic.Message, error) {
	return a.client.Messages.New(ctx, p)
}

// Caller forwards a tool call to an implementation.
type Caller interface {
	Call(ctx context.Context, name string, args json.RawMessage) (text string, isError bool, err error)
}

// Settings are the model parameters every phase records.
type Settings struct {
	Model     string `json:"model"`
	Effort    string `json:"effort"`
	MaxTokens int64  `json:"max_tokens"`
	MaxTurns  int    `json:"max_turns"`
}

// Usage is token use, summed and per call. InputTokens includes cached
// input, which is also broken out.
type Usage struct {
	Calls            int         `json:"calls"`
	InputTokens      int64       `json:"input_tokens"`
	OutputTokens     int64       `json:"output_tokens"`
	CacheReadTokens  int64       `json:"cache_read_tokens"`
	CacheWriteTokens int64       `json:"cache_write_tokens"`
	PerCall          []CallUsage `json:"per_call,omitempty"`
}

// CallUsage is one request's tokens.
type CallUsage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

// Add accumulates u2 into u.
func (u *Usage) Add(u2 Usage) {
	u.Calls += u2.Calls
	u.InputTokens += u2.InputTokens
	u.OutputTokens += u2.OutputTokens
	u.CacheReadTokens += u2.CacheReadTokens
	u.CacheWriteTokens += u2.CacheWriteTokens
	u.PerCall = append(u.PerCall, u2.PerCall...)
}

func (u *Usage) record(m anthropic.Usage) {
	c := CallUsage{
		InputTokens:      m.InputTokens + m.CacheReadInputTokens + m.CacheCreationInputTokens,
		OutputTokens:     m.OutputTokens,
		CacheReadTokens:  m.CacheReadInputTokens,
		CacheWriteTokens: m.CacheCreationInputTokens,
	}
	u.Calls++
	u.InputTokens += c.InputTokens
	u.OutputTokens += c.OutputTokens
	u.CacheReadTokens += c.CacheReadTokens
	u.CacheWriteTokens += c.CacheWriteTokens
	u.PerCall = append(u.PerCall, c)
}

// Result is the outcome of one loop.
type Result struct {
	// Text is the text of the final response.
	Text       string         `json:"text"`
	StopReason string         `json:"stop_reason"`
	Turns      int            `json:"turns"`
	Capped     bool           `json:"capped,omitempty"`
	ToolCalls  map[string]int `json:"tool_calls,omitempty"`
	ToolErrors int            `json:"tool_errors,omitempty"`
	Usage      Usage          `json:"usage"`
}

// RefusalError is a response that stopped with stop_reason "refusal".
type RefusalError struct{ Category, Explanation string }

func (e *RefusalError) Error() string {
	return fmt.Sprintf("model refused (%s): %s", e.Category, e.Explanation)
}

// Request is one loop's inputs.
type Request struct {
	Settings
	// Instructions is the server's initialize instructions, sent first in
	// the system prompt; Phase follows it.
	Instructions string
	Phase        string
	User         string
	Tools        []*mcp.Tool
}

// Run drives the loop until the model stops calling tools or MaxTurns model
// calls have been made. Tool-level errors go back to the model as the server
// returned them; a protocol error ends the loop with an error.
func Run(ctx context.Context, m Model, c Caller, req Request) (Result, error) {
	res := Result{ToolCalls: map[string]int{}}
	tools, err := impl.MessagesTools(req.Tools)
	if err != nil {
		return res, err
	}
	var system []anthropic.TextBlockParam
	if req.Instructions != "" {
		system = append(system, anthropic.TextBlockParam{Text: req.Instructions})
	}
	if req.Phase != "" {
		system = append(system, anthropic.TextBlockParam{Text: req.Phase})
	}
	if len(system) > 0 {
		// Tools and system are identical on every turn: cache them.
		system[len(system)-1].CacheControl = anthropic.NewCacheControlEphemeralParam()
	}
	messages := []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(req.User))}

	for {
		if req.MaxTurns > 0 && res.Turns >= req.MaxTurns {
			res.Capped = true
			return res, nil
		}
		params := anthropic.MessageNewParams{
			Model:     anthropic.Model(req.Model),
			MaxTokens: req.MaxTokens,
			System:    system,
			Messages:  messages,
			Tools:     tools,
		}
		if req.Effort != "" {
			params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(req.Effort)}
		}
		msg, err := m.New(ctx, params)
		if err != nil {
			return res, err
		}
		res.Turns++
		res.Usage.record(msg.Usage)
		res.StopReason = string(msg.StopReason)
		if msg.StopReason == anthropic.StopReasonRefusal {
			return res, &RefusalError{Category: string(msg.StopDetails.Category), Explanation: msg.StopDetails.Explanation}
		}
		messages = append(messages, msg.ToParam())

		var text []string
		var results []anthropic.ContentBlockParamUnion
		for _, block := range msg.Content {
			switch b := block.AsAny().(type) {
			case anthropic.TextBlock:
				text = append(text, b.Text)
			case anthropic.ToolUseBlock:
				res.ToolCalls[b.Name]++
				out, isErr, err := c.Call(ctx, b.Name, json.RawMessage(b.JSON.Input.Raw()))
				if err != nil {
					return res, fmt.Errorf("tool %s: %w", b.Name, err)
				}
				if isErr {
					res.ToolErrors++
				}
				results = append(results, anthropic.NewToolResultBlock(b.ID, out, isErr))
			}
		}
		res.Text = strings.TrimSpace(strings.Join(text, "\n"))
		if msg.StopReason != anthropic.StopReasonToolUse || len(results) == 0 {
			return res, nil
		}
		messages = append(messages, anthropic.NewUserMessage(results...))
	}
}

// Complete sends a single request with no tools and returns its text.
func Complete(ctx context.Context, m Model, s Settings, system, user string) (string, Usage, error) {
	res, err := Run(ctx, m, nil, Request{Settings: s, Phase: system, User: user})
	return res.Text, res.Usage, err
}
