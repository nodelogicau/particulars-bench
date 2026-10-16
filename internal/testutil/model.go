// Package testutil holds test doubles shared across packages.
package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
)

// Reply is a scripted assistant turn: text, then tool calls.
type Reply struct {
	Text  string
	Tools []ToolUse
	// Usage tokens reported for the turn (input, output).
	In, Out int64
	// Refuse ends the turn with stop_reason "refusal".
	Refuse bool
}

// ToolUse is one scripted tool call.
type ToolUse struct {
	Name  string
	Input map[string]any
}

// Model is an agent.Provider, through the Anthropic adapter, whose replies come from Script. It records every request.
type Model struct {
	Script func(p anthropic.MessageNewParams) Reply
	mu     sync.Mutex
	Calls  []anthropic.MessageNewParams
}

// New implements agent.MessagesClient.
func (m *Model) New(_ context.Context, p anthropic.MessageNewParams) (*anthropic.Message, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, p)
	n := len(m.Calls)
	m.mu.Unlock()
	r := m.Script(p)
	return Message(r, n)
}

// CallCount returns the number of requests made.
func (m *Model) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls)
}

// Message builds an API response from a Reply.
func Message(r Reply, n int) (*anthropic.Message, error) {
	var content []map[string]any
	if r.Text != "" {
		content = append(content, map[string]any{"type": "text", "text": r.Text})
	}
	for i, t := range r.Tools {
		in := t.Input
		if in == nil {
			in = map[string]any{}
		}
		content = append(content, map[string]any{"type": "tool_use", "id": fmt.Sprintf("toolu_%d_%d", n, i), "name": t.Name, "input": in})
	}
	stop := "end_turn"
	if len(r.Tools) > 0 {
		stop = "tool_use"
	}
	if r.Refuse {
		stop = "refusal"
	}
	in, out := r.In, r.Out
	if in == 0 && out == 0 {
		in, out = 100, 10
	}
	b, err := json.Marshal(map[string]any{
		"id": fmt.Sprintf("msg_%d", n), "type": "message", "role": "assistant", "model": "fake",
		"content": content, "stop_reason": stop,
		"usage": map[string]any{"input_tokens": in, "output_tokens": out},
	})
	if err != nil {
		return nil, err
	}
	var msg anthropic.Message
	return &msg, json.Unmarshal(b, &msg)
}

// ToolNames lists the names of the tools offered in a request.
func ToolNames(p anthropic.MessageNewParams) []string {
	var out []string
	for _, t := range p.Tools {
		if t.OfTool != nil {
			out = append(out, t.OfTool.Name)
		}
	}
	return out
}

// Has reports whether names contains name.
func Has(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
