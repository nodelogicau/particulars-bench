// Package agent runs the tool-use loop against an MCP server through a model
// provider: the model sees the server's tools and instructions as served,
// every call is forwarded, and tokens and tool calls are counted.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Providers.
const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
)

// Providers lists the provider names.
var Providers = []string{ProviderAnthropic, ProviderOpenAI}

// ToolDef is a tool as the server lists it: name, description, and input
// schema. Each provider renders it in its own format, unchanged.
type ToolDef struct {
	Name        string
	Description string
	Schema      map[string]any
}

// ToolCall is one tool call the model made. Args is the model's raw JSON,
// which need not be valid.
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

// ToolResult answers one tool call.
type ToolResult struct {
	ID      string
	Text    string
	IsError bool
}

// Turn is one model response.
type Turn struct {
	Text      string
	ToolCalls []ToolCall
	// StopReason is the provider's own stop or finish reason.
	StopReason string
	// More reports that the model is waiting for its tool results.
	More bool
	// Truncated reports that the response stopped at the output limit.
	Truncated bool
	Usage     CallUsage
}

// Provider starts conversations with one model endpoint.
type Provider interface {
	Start(s Settings, system []string, user string, tools []ToolDef) (Conversation, error)
}

// Conversation is one loop's history, which the provider keeps in its own
// form so that it is replayed without conversion.
type Conversation interface {
	// Send requests the next response. A response the provider declined
	// to give returns its Turn with a *RefusalError.
	Send(ctx context.Context) (Turn, error)
	// AddToolResults answers the tool calls of the last response.
	AddToolResults(results []ToolResult)
}

// Caller forwards a tool call to an implementation.
type Caller interface {
	Call(ctx context.Context, name string, args json.RawMessage) (text string, isError bool, err error)
}

// Settings are the model parameters every phase records. The provider
// fields are omitted at the Anthropic defaults, so that settings recorded
// (and cache keys computed) before providers existed are unchanged.
type Settings struct {
	// Provider is empty for anthropic.
	Provider string `json:"provider,omitempty"`
	// BaseURL is empty for the provider's SDK default.
	BaseURL   string `json:"base_url,omitempty"`
	Model     string `json:"model"`
	Effort    string `json:"effort"`
	MaxTokens int64  `json:"max_tokens"`
	MaxTurns  int    `json:"max_turns"`
	// Temperature is sent only when set.
	Temperature *float64 `json:"temperature,omitempty"`
	// ExtraBody is merged into every request body unchanged.
	ExtraBody map[string]any `json:"extra_body,omitempty"`
}

// ProviderName returns the provider, anthropic when unset.
func (s Settings) ProviderName() string {
	if s.Provider == "" {
		return ProviderAnthropic
	}
	return s.Provider
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

// CallUsage is one request's tokens, and the upstream that served it when
// the response names one.
type CallUsage struct {
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	Upstream         string `json:"upstream,omitempty"`
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

func (u *Usage) record(c CallUsage) {
	u.Calls++
	u.InputTokens += c.InputTokens
	u.OutputTokens += c.OutputTokens
	u.CacheReadTokens += c.CacheReadTokens
	u.CacheWriteTokens += c.CacheWriteTokens
	u.PerCall = append(u.PerCall, c)
}

// Upstreams returns the distinct upstreams named by the calls.
func (u Usage) Upstreams() []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range u.PerCall {
		if c.Upstream != "" && !seen[c.Upstream] {
			seen[c.Upstream] = true
			out = append(out, c.Upstream)
		}
	}
	return out
}

// Result is the outcome of one loop.
type Result struct {
	// Text is the text of the final response.
	Text       string `json:"text"`
	StopReason string `json:"stop_reason"`
	Turns      int    `json:"turns"`
	// Capped reports that the turn cap, or an output limit the provider
	// reports as truncation, ended the loop.
	Capped     bool           `json:"capped,omitempty"`
	ToolCalls  map[string]int `json:"tool_calls,omitempty"`
	ToolErrors int            `json:"tool_errors,omitempty"`
	Usage      Usage          `json:"usage"`
}

// RefusalError is a response the provider declined to give (Anthropic
// stop_reason "refusal", OpenAI finish_reason "content_filter").
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
	Tools        []ToolDef
}

// Run drives the loop until the model stops calling tools or MaxTurns model
// calls have been made. Tool-level errors go back to the model as the server
// returned them, and calls of a tool not offered or with arguments that are
// not valid JSON go back as an error result without reaching the server; a
// protocol error ends the loop with an error.
func Run(ctx context.Context, p Provider, c Caller, req Request) (Result, error) {
	res := Result{ToolCalls: map[string]int{}}
	offered := map[string]bool{}
	for _, t := range req.Tools {
		offered[t.Name] = true
	}
	var system []string
	if req.Instructions != "" {
		system = append(system, req.Instructions)
	}
	if req.Phase != "" {
		system = append(system, req.Phase)
	}
	conv, err := p.Start(req.Settings, system, req.User, req.Tools)
	if err != nil {
		return res, err
	}
	for {
		if req.MaxTurns > 0 && res.Turns >= req.MaxTurns {
			res.Capped = true
			return res, nil
		}
		turn, err := conv.Send(ctx)
		var refusal *RefusalError
		if err != nil && !errors.As(err, &refusal) {
			return res, err
		}
		res.Turns++
		res.Usage.record(turn.Usage)
		res.StopReason = turn.StopReason
		if refusal != nil {
			return res, err
		}
		res.Text = strings.TrimSpace(turn.Text)
		if turn.Truncated {
			res.Capped = true
		}

		var results []ToolResult
		for _, call := range turn.ToolCalls {
			res.ToolCalls[call.Name]++
			if !offered[call.Name] {
				res.ToolErrors++
				results = append(results, ToolResult{ID: call.ID, IsError: true,
					Text: fmt.Sprintf("unknown tool %q: not one of the tools offered", call.Name)})
				continue
			}
			if !json.Valid(call.Args) {
				res.ToolErrors++
				results = append(results, ToolResult{ID: call.ID, IsError: true,
					Text: fmt.Sprintf("invalid tool arguments: not a JSON value: %s", call.Args)})
				continue
			}
			out, isErr, err := c.Call(ctx, call.Name, call.Args)
			if err != nil {
				return res, fmt.Errorf("tool %s: %w", call.Name, err)
			}
			if isErr {
				res.ToolErrors++
			}
			results = append(results, ToolResult{ID: call.ID, Text: out, IsError: isErr})
		}
		if !turn.More || len(results) == 0 {
			return res, nil
		}
		conv.AddToolResults(results)
	}
}

// Complete sends a single request with no tools and returns its text.
func Complete(ctx context.Context, p Provider, s Settings, system, user string) (string, Usage, error) {
	res, err := Run(ctx, p, nil, Request{Settings: s, Phase: system, User: user})
	return res.Text, res.Usage, err
}

// New returns the provider for s, authenticated with apiKey.
func New(s Settings, apiKey string) (Provider, error) {
	switch s.ProviderName() {
	case ProviderAnthropic:
		return NewAnthropic(anthropicOptions(s, apiKey)...), nil
	case ProviderOpenAI:
		return NewOpenAI(openAIOptions(s, apiKey)...), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (known: %v)", s.Provider, Providers)
	}
}
