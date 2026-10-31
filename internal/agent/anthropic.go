package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// MessagesClient sends one Messages API request.
type MessagesClient interface {
	New(ctx context.Context, p anthropic.MessageNewParams) (*anthropic.Message, error)
}

type sdkMessages struct{ client anthropic.Client }

func (s sdkMessages) New(ctx context.Context, p anthropic.MessageNewParams) (*anthropic.Message, error) {
	return s.client.Messages.New(ctx, p)
}

// Anthropic is the provider for the Anthropic Messages API.
type Anthropic struct{ client MessagesClient }

// NewAnthropic returns a provider with the SDK's default retries.
func NewAnthropic(opts ...option.RequestOption) *Anthropic {
	return &Anthropic{client: sdkMessages{anthropic.NewClient(opts...)}}
}

// AnthropicWith returns a provider that sends requests through c.
func AnthropicWith(c MessagesClient) *Anthropic { return &Anthropic{client: c} }

func anthropicOptions(s Settings, apiKey string) []option.RequestOption {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if s.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(s.BaseURL))
	}
	return opts
}

// Start implements Provider. The system prompt and tools are identical on
// every turn and are marked for prompt caching. With tools, which make a loop
// of more than one turn, the conversation so far is marked too, since each
// turn resends it with its tool results; without, it would be written to the
// cache and never read.
func (a *Anthropic) Start(s Settings, system []string, user string, tools []ToolDef) (Conversation, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(s.Model),
		MaxTokens: s.MaxTokens,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(user))},
	}
	if len(tools) > 0 {
		params.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}
	for _, t := range tools {
		at, err := AnthropicTool(t)
		if err != nil {
			return nil, err
		}
		params.Tools = append(params.Tools, at)
	}
	for _, text := range system {
		params.System = append(params.System, anthropic.TextBlockParam{Text: text})
	}
	if len(params.System) > 0 {
		params.System[len(params.System)-1].CacheControl = anthropic.NewCacheControlEphemeralParam()
	}
	if s.Effort != "" {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(s.Effort)}
	}
	if s.Temperature != nil {
		params.Temperature = anthropic.Float(*s.Temperature)
	}
	if len(s.ExtraBody) > 0 {
		params.SetExtraFields(s.ExtraBody)
	}
	return &anthropicConversation{client: a.client, params: params}, nil
}

type anthropicConversation struct {
	client MessagesClient
	params anthropic.MessageNewParams
}

func (c *anthropicConversation) Send(ctx context.Context) (Turn, error) {
	msg, err := c.client.New(ctx, c.params)
	if err != nil {
		return Turn{}, err
	}
	m := msg.Usage
	turn := Turn{
		StopReason: string(msg.StopReason),
		More:       msg.StopReason == anthropic.StopReasonToolUse,
		Usage: CallUsage{
			InputTokens:      m.InputTokens + m.CacheReadInputTokens + m.CacheCreationInputTokens,
			OutputTokens:     m.OutputTokens,
			CacheReadTokens:  m.CacheReadInputTokens,
			CacheWriteTokens: m.CacheCreationInputTokens,
		},
	}
	if msg.StopReason == anthropic.StopReasonRefusal {
		return turn, &RefusalError{Category: string(msg.StopDetails.Category), Explanation: msg.StopDetails.Explanation}
	}
	// The response replays as given, thinking blocks included.
	c.params.Messages = append(c.params.Messages, msg.ToParam())
	var text []string
	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			text = append(text, b.Text)
		case anthropic.ToolUseBlock:
			turn.ToolCalls = append(turn.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Args: json.RawMessage(b.JSON.Input.Raw())})
		}
	}
	turn.Text = strings.Join(text, "\n")
	return turn, nil
}

func (c *anthropicConversation) AddToolResults(results []ToolResult) {
	blocks := make([]anthropic.ContentBlockParamUnion, len(results))
	for i, r := range results {
		blocks[i] = anthropic.NewToolResultBlock(r.ID, r.Text, r.IsError)
	}
	c.params.Messages = append(c.params.Messages, anthropic.NewUserMessage(blocks...))
}

// AnthropicTool renders a tool as a Messages API tool with its name,
// description, and input schema unchanged.
func AnthropicTool(t ToolDef) (anthropic.ToolUnionParam, error) {
	in := anthropic.ToolInputSchemaParam{ExtraFields: map[string]any{}}
	for k, v := range t.Schema {
		switch k {
		case "type":
			if v != "object" {
				return anthropic.ToolUnionParam{}, fmt.Errorf("tool %s: input schema type %v is not object", t.Name, v)
			}
		case "properties":
			in.Properties = v
		case "required":
			req, ok := v.([]any)
			if !ok {
				return anthropic.ToolUnionParam{}, fmt.Errorf("tool %s: required is not an array", t.Name)
			}
			for _, r := range req {
				s, ok := r.(string)
				if !ok {
					return anthropic.ToolUnionParam{}, fmt.Errorf("tool %s: required entry %v is not a string", t.Name, r)
				}
				in.Required = append(in.Required, s)
			}
		default:
			in.ExtraFields[k] = v
		}
	}
	tp := anthropic.ToolParam{Name: t.Name, InputSchema: in}
	if t.Description != "" {
		tp.Description = anthropic.String(t.Description)
	}
	return anthropic.ToolUnionParam{OfTool: &tp}, nil
}
