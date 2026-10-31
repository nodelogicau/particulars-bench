package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/testutil"
)

type caller struct {
	calls []string
	args  []string
}

func (c *caller) Call(_ context.Context, name string, args json.RawMessage) (string, bool, error) {
	c.calls = append(c.calls, name)
	c.args = append(c.args, string(args))
	return "result of " + name, name == "fails", nil
}

var tools = []agent.ToolDef{
	{Name: "knowledge_recall", Description: "Recall.", Schema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}},
	{Name: "fails", Schema: map[string]any{"type": "object"}},
}

func TestRunForwardsAndCounts(t *testing.T) {
	m := &testutil.Model{Script: func(p anthropic.MessageNewParams) testutil.Reply {
		switch len(p.Messages) {
		case 1:
			return testutil.Reply{Tools: []testutil.ToolUse{{Name: "knowledge_recall", Input: map[string]any{"query": "bike"}}, {Name: "fails"}}, In: 50, Out: 5}
		default:
			return testutil.Reply{Text: "red", In: 70, Out: 3}
		}
	}}
	c := &caller{}
	res, err := agent.Run(context.Background(), m, c, agent.Request{
		Settings:     agent.Settings{Model: "claude-opus-5-5", Effort: "medium", MaxTokens: 1000, MaxTurns: 5},
		Instructions: "SERVER INSTRUCTIONS", Phase: "PHASE", User: "Q", Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "red" || res.Turns != 2 || res.Capped {
		t.Errorf("result %+v", res)
	}
	if res.ToolCalls["knowledge_recall"] != 1 || res.ToolCalls["fails"] != 1 || res.ToolErrors != 1 ||
		len(res.ToolErrorsByTool) != 1 || res.ToolErrorsByTool["fails"] != 1 {
		t.Errorf("tool calls %v errors %d by tool %v", res.ToolCalls, res.ToolErrors, res.ToolErrorsByTool)
	}
	if c.args[0] != `{"query":"bike"}` {
		t.Errorf("arguments not forwarded: %s", c.args[0])
	}
	if res.Usage.Calls != 2 || res.Usage.InputTokens != 120 || res.Usage.OutputTokens != 8 || len(res.Usage.PerCall) != 2 {
		t.Errorf("usage %+v", res.Usage)
	}
	first := m.Calls[0]
	if len(first.System) != 2 || first.System[0].Text != "SERVER INSTRUCTIONS" || first.System[1].Text != "PHASE" {
		t.Errorf("system prompt %+v", first.System)
	}
	if first.OutputConfig.Effort != "medium" || first.Model != "claude-opus-5-5" {
		t.Errorf("settings not sent: %+v", first.OutputConfig)
	}
	if first.System[1].CacheControl.Type == "" || m.Calls[1].CacheControl.Type == "" {
		t.Errorf("cache markers: system %+v, conversation %+v", first.System[1].CacheControl, m.Calls[1].CacheControl)
	}
	// Both tool results return in one user message after the assistant turn.
	second := m.Calls[1]
	if len(second.Messages) != 3 || len(second.Messages[2].Content) != 2 {
		t.Fatalf("second request messages: %d", len(second.Messages))
	}
	tr := second.Messages[2].Content[1].OfToolResult
	if tr == nil || !tr.IsError.Value {
		t.Errorf("tool error not passed back as is_error")
	}
}

func TestRunCapsTurns(t *testing.T) {
	m := &testutil.Model{Script: func(p anthropic.MessageNewParams) testutil.Reply {
		return testutil.Reply{Tools: []testutil.ToolUse{{Name: "knowledge_recall"}}}
	}}
	res, err := agent.Run(context.Background(), m, &caller{}, agent.Request{
		Settings: agent.Settings{Model: "m", MaxTokens: 100, MaxTurns: 3}, User: "Q", Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Capped || res.Turns != 3 || m.CallCount() != 3 {
		t.Errorf("capped %t turns %d calls %d", res.Capped, res.Turns, m.CallCount())
	}
}

type failingCaller struct{}

func (failingCaller) Call(context.Context, string, json.RawMessage) (string, bool, error) {
	return "", true, errors.New("connection closed")
}

func TestRunProtocolErrorEndsLoop(t *testing.T) {
	m := &testutil.Model{Script: func(p anthropic.MessageNewParams) testutil.Reply {
		return testutil.Reply{Tools: []testutil.ToolUse{{Name: "knowledge_recall"}}}
	}}
	_, err := agent.Run(context.Background(), m, failingCaller{}, agent.Request{
		Settings: agent.Settings{Model: "m", MaxTokens: 100, MaxTurns: 3}, User: "Q", Tools: tools,
	})
	if err == nil {
		t.Fatal("protocol error swallowed")
	}
}

// scripted is a Provider whose turns are fixed, for provider-neutral loop
// behaviour.
type scripted struct {
	turns   []agent.Turn
	results [][]agent.ToolResult
}

func (s *scripted) Start(agent.Settings, []string, string, []agent.ToolDef) (agent.Conversation, error) {
	return s, nil
}

func (s *scripted) Send(context.Context) (agent.Turn, error) {
	t := s.turns[0]
	s.turns = s.turns[1:]
	return t, nil
}

func (s *scripted) AddToolResults(r []agent.ToolResult) { s.results = append(s.results, r) }

func TestRunInvalidArgumentsAreToolErrors(t *testing.T) {
	p := &scripted{turns: []agent.Turn{
		{More: true, ToolCalls: []agent.ToolCall{{ID: "1", Name: "knowledge_recall", Args: []byte(`{"query": "bi`)}}},
		{Text: "red"},
	}}
	c := &caller{}
	res, err := agent.Run(context.Background(), p, c, agent.Request{Settings: agent.Settings{MaxTurns: 5}, User: "Q", Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 0 {
		t.Errorf("invalid arguments reached the server: %v", c.calls)
	}
	if res.ToolCalls["knowledge_recall"] != 1 || res.ToolErrors != 1 || res.ToolErrorsByTool["knowledge_recall"] != 1 || res.Text != "red" {
		t.Errorf("result %+v", res)
	}
	if r := p.results[0][0]; r.ID != "1" || !r.IsError || !strings.Contains(r.Text, "invalid tool arguments") {
		t.Errorf("tool result %+v", r)
	}
}

func TestRunUnknownToolsAreToolErrors(t *testing.T) {
	p := &scripted{turns: []agent.Turn{
		{More: true, ToolCalls: []agent.ToolCall{{ID: "1", Name: "particulars_register", Args: []byte(`{}`)}}},
		{Text: "red"},
	}}
	c := &caller{}
	res, err := agent.Run(context.Background(), p, c, agent.Request{Settings: agent.Settings{MaxTurns: 5}, User: "Q", Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 0 {
		t.Errorf("unknown tool reached the server: %v", c.calls)
	}
	if res.ToolCalls["particulars_register"] != 1 || res.ToolErrors != 1 || res.ToolErrorsByTool["particulars_register"] != 1 || res.Text != "red" {
		t.Errorf("result %+v", res)
	}
	if r := p.results[0][0]; r.ID != "1" || !r.IsError || !strings.Contains(r.Text, "unknown tool") {
		t.Errorf("tool result %+v", r)
	}
}

func TestRunRefusalIsFailure(t *testing.T) {
	m := &testutil.Model{Script: func(anthropic.MessageNewParams) testutil.Reply { return testutil.Reply{Refuse: true} }}
	res, err := agent.Run(context.Background(), m, &caller{}, agent.Request{Settings: agent.Settings{Model: "m", MaxTurns: 3}, User: "Q"})
	var refusal *agent.RefusalError
	if !errors.As(err, &refusal) || res.Turns != 1 || res.Usage.Calls != 1 {
		t.Errorf("refusal: err %v, result %+v", err, res)
	}
	// A request without tools is one turn; its conversation is not cached.
	if m.Calls[0].CacheControl.Type != "" {
		t.Errorf("conversation cached without tools: %+v", m.Calls[0].CacheControl)
	}
}
