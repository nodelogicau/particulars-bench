package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

// DefaultOpenAIBaseURL is the base URL of the openai provider when none is
// given.
const DefaultOpenAIBaseURL = "https://api.openai.com/v1"

// OpenAI is the provider for OpenAI-compatible Chat Completions endpoints:
// OpenAI, routers, and local servers.
type OpenAI struct{ client openai.Client }

// NewOpenAI returns a provider with the SDK's default retries.
func NewOpenAI(opts ...option.RequestOption) *OpenAI {
	return &OpenAI{client: openai.NewClient(opts...)}
}

func openAIOptions(s Settings, apiKey string) []option.RequestOption {
	base := s.BaseURL
	if base == "" {
		base = DefaultOpenAIBaseURL
	}
	return []option.RequestOption{option.WithBaseURL(base), option.WithAPIKey(apiKey)}
}

// Start implements Provider. The system prompt is one system message, its
// parts separated by a blank line. Provider parameters are sent only when
// set: reasoning_effort for an effort, max_completion_tokens for a positive
// MaxTokens, temperature when given.
func (o *OpenAI) Start(s Settings, system []string, user string, tools []ToolDef) (Conversation, error) {
	params := openai.ChatCompletionNewParams{Model: shared.ChatModel(s.Model)}
	if len(system) > 0 {
		params.Messages = append(params.Messages, openai.SystemMessage(strings.Join(system, "\n\n")))
	}
	params.Messages = append(params.Messages, openai.UserMessage(user))
	for _, t := range tools {
		params.Tools = append(params.Tools, OpenAITool(t))
	}
	if s.Effort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(s.Effort)
	}
	if s.MaxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(s.MaxTokens)
	}
	if s.Temperature != nil {
		params.Temperature = openai.Float(*s.Temperature)
	}
	if len(s.ExtraBody) > 0 {
		params.SetExtraFields(s.ExtraBody)
	}
	return &openAIConversation{client: o.client, params: params}, nil
}

type openAIConversation struct {
	client openai.Client
	params openai.ChatCompletionNewParams
}

func (c *openAIConversation) Send(ctx context.Context) (Turn, error) {
	resp, err := c.client.Chat.Completions.New(ctx, c.params)
	if err != nil {
		return Turn{}, withServerMessage(err)
	}
	u := resp.Usage
	turn := Turn{Usage: CallUsage{
		InputTokens:     u.PromptTokens,
		OutputTokens:    u.CompletionTokens,
		CacheReadTokens: u.PromptTokensDetails.CachedTokens,
		Upstream:        upstream(resp),
	}}
	if len(resp.Choices) == 0 {
		return turn, fmt.Errorf("chat completion %s has no choices", resp.ID)
	}
	choice := resp.Choices[0]
	turn.StopReason = choice.FinishReason
	if choice.FinishReason == "content_filter" {
		return turn, &RefusalError{Category: "content_filter", Explanation: choice.Message.Refusal}
	}
	c.params.Messages = append(c.params.Messages, choice.Message.ToParam())
	turn.Text = choice.Message.Content
	for _, tc := range choice.Message.ToolCalls {
		if tc.Type != "" && tc.Type != "function" {
			continue
		}
		args := tc.Function.Arguments
		if strings.TrimSpace(args) == "" {
			// Some servers send no arguments for a tool without parameters.
			args = "{}"
		}
		turn.ToolCalls = append(turn.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Args: json.RawMessage(args)})
	}
	switch choice.FinishReason {
	case "length":
		turn.Truncated = true
	default:
		// Some servers finish with "stop" while returning tool calls; the
		// calls are what the model asked for.
		turn.More = len(turn.ToolCalls) > 0
	}
	return turn, nil
}

// AddToolResults appends one tool message per result. Chat Completions has
// no error flag, so an error result is its text alone, as the server
// returned it.
func (c *openAIConversation) AddToolResults(results []ToolResult) {
	for _, r := range results {
		c.params.Messages = append(c.params.Messages, openai.ToolMessage(r.Text, r.ID))
	}
}

// withServerMessage adds the server's error message, which the SDK's error
// text leaves out.
func withServerMessage(err error) error {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	msg := apiErr.Message
	if msg == "" {
		msg = apiErr.RawJSON()
	}
	if msg == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, msg)
}

// upstream returns the response's top-level "provider" field, which routers
// such as OpenRouter set to the upstream that served the request.
func upstream(resp *openai.ChatCompletion) string {
	f, ok := resp.JSON.ExtraFields["provider"]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal([]byte(f.Raw()), &s); err == nil {
		return s
	}
	return f.Raw()
}

// OpenAITool renders a tool as a function with its name, description, and
// input schema as parameters, unchanged, and strict mode off.
func OpenAITool(t ToolDef) openai.ChatCompletionToolUnionParam {
	fn := shared.FunctionDefinitionParam{Name: t.Name, Parameters: shared.FunctionParameters(t.Schema)}
	if t.Description != "" {
		fn.Description = openai.String(t.Description)
	}
	return openai.ChatCompletionFunctionTool(fn)
}
