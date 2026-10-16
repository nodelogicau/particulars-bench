package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/nodelogicau/particulars-bench/internal/agent"
)

// chatServer speaks Chat Completions: it records each request body and
// answers with the next scripted response.
type chatServer struct {
	t         *testing.T
	mu        sync.Mutex
	requests  []map[string]any
	auth      []string
	responses []any
	status    int
}

func (s *chatServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.URL.Path != "/v1/chat/completions" {
		s.t.Errorf("path %s", r.URL.Path)
	}
	b, _ := io.ReadAll(r.Body)
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		s.t.Errorf("request body: %v", err)
	}
	s.requests = append(s.requests, body)
	s.auth = append(s.auth, r.Header.Get("Authorization"))
	w.Header().Set("Content-Type", "application/json")
	if s.status != 0 {
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(`{"error":{"message":"tools are not supported by this model","type":"invalid_request_error"}}`))
		return
	}
	resp := s.responses[0]
	s.responses = s.responses[1:]
	_ = json.NewEncoder(w).Encode(resp)
}

func completion(provider, finish string, msg map[string]any, prompt, cached, out int) map[string]any {
	msg["role"] = "assistant"
	c := map[string]any{
		"id": "chatcmpl-1", "object": "chat.completion", "created": 1, "model": "m",
		"choices": []any{map[string]any{"index": 0, "finish_reason": finish, "message": msg}},
		"usage": map[string]any{
			"prompt_tokens": prompt, "completion_tokens": out, "total_tokens": prompt + out,
			"prompt_tokens_details": map[string]any{"cached_tokens": cached},
		},
	}
	if provider != "" {
		c["provider"] = provider
	}
	return c
}

func openAI(t *testing.T, s *chatServer, settings agent.Settings) (agent.Provider, agent.Settings) {
	t.Helper()
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	settings.Provider, settings.BaseURL = agent.ProviderOpenAI, srv.URL+"/v1"
	p, err := agent.New(settings, "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	return p, settings
}

func TestOpenAIToolRoundTrip(t *testing.T) {
	s := &chatServer{t: t, responses: []any{
		completion("Upstream-A", "tool_calls", map[string]any{"content": nil, "tool_calls": []any{
			map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "knowledge_recall", "arguments": `{"query":"bike"}`}},
			map[string]any{"id": "call_2", "type": "function", "function": map[string]any{"name": "knowledge_recall", "arguments": `{"query":`}},
		}}, 50, 20, 5),
		completion("Upstream-B", "stop", map[string]any{"content": "red"}, 70, 40, 3),
	}}
	zero := 0.0
	extra := map[string]any{"provider": map[string]any{"order": []any{"x"}, "allow_fallbacks": false}}
	p, settings := openAI(t, s, agent.Settings{Model: "gpt-4o", MaxTokens: 1000, MaxTurns: 5, Temperature: &zero, ExtraBody: extra})
	c := &caller{}
	res, err := agent.Run(context.Background(), p, c, agent.Request{
		Settings: settings, Instructions: "SERVER INSTRUCTIONS", Phase: "PHASE", User: "Q", Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}

	if res.Text != "red" || res.Turns != 2 || res.StopReason != "stop" || res.Capped {
		t.Errorf("result %+v", res)
	}
	if len(c.args) != 1 || c.args[0] != `{"query":"bike"}` {
		t.Errorf("forwarded calls %v", c.args)
	}
	if res.ToolCalls["knowledge_recall"] != 2 || res.ToolErrors != 1 {
		t.Errorf("tool calls %v errors %d", res.ToolCalls, res.ToolErrors)
	}
	u := res.Usage
	if u.Calls != 2 || u.InputTokens != 120 || u.CacheReadTokens != 60 || u.OutputTokens != 8 || u.CacheWriteTokens != 0 {
		t.Errorf("usage %+v", u)
	}
	if got := u.Upstreams(); !reflect.DeepEqual(got, []string{"Upstream-A", "Upstream-B"}) {
		t.Errorf("upstreams %v", got)
	}

	first := s.requests[0]
	if s.auth[0] != "Bearer sk-test" {
		t.Errorf("authorization %q", s.auth[0])
	}
	if first["model"] != "gpt-4o" || first["max_completion_tokens"] != 1000.0 || first["temperature"] != 0.0 {
		t.Errorf("parameters %v", first)
	}
	if _, ok := first["reasoning_effort"]; ok {
		t.Error("reasoning_effort sent without an effort")
	}
	if !reflect.DeepEqual(first["provider"], extra["provider"]) {
		t.Errorf("extra body not merged: %v", first["provider"])
	}
	msgs := first["messages"].([]any)
	if sys := msgs[0].(map[string]any); sys["role"] != "system" || sys["content"] != "SERVER INSTRUCTIONS\n\nPHASE" {
		t.Errorf("system message %v", sys)
	}
	fn := first["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "knowledge_recall" || fn["description"] != "Recall." || !reflect.DeepEqual(fn["parameters"], tools[0].Schema) {
		t.Errorf("function %v", fn)
	}
	if _, ok := fn["strict"]; ok {
		t.Error("strict set")
	}

	// The second request replays the assistant's tool calls, then one tool
	// message per call, in order.
	msgs = s.requests[1]["messages"].([]any)
	if len(msgs) != 5 {
		t.Fatalf("second request has %d messages", len(msgs))
	}
	if calls := msgs[2].(map[string]any)["tool_calls"].([]any); len(calls) != 2 {
		t.Errorf("assistant tool calls %v", calls)
	}
	t1, t2 := msgs[3].(map[string]any), msgs[4].(map[string]any)
	if t1["role"] != "tool" || t1["tool_call_id"] != "call_1" || t1["content"] != "result of knowledge_recall" {
		t.Errorf("tool message %v", t1)
	}
	if t2["tool_call_id"] != "call_2" || !strings.Contains(t2["content"].(string), "invalid tool arguments") {
		t.Errorf("invalid-arguments tool message %v", t2)
	}
	if !reflect.DeepEqual(s.requests[1]["provider"], extra["provider"]) {
		t.Error("extra body missing from a later request")
	}
}

func TestOpenAIEffortAndStops(t *testing.T) {
	s := &chatServer{t: t, responses: []any{
		completion("", "length", map[string]any{"content": "partial"}, 10, 0, 1),
		completion("", "content_filter", map[string]any{"content": nil, "refusal": "no"}, 10, 0, 1),
	}}
	p, settings := openAI(t, s, agent.Settings{Model: "o3", Effort: "low", MaxTurns: 3})
	res, err := agent.Run(context.Background(), p, nil, agent.Request{Settings: settings, User: "Q"})
	if err != nil || !res.Capped || res.Text != "partial" {
		t.Errorf("length: err %v, result %+v", err, res)
	}
	if s.requests[0]["reasoning_effort"] != "low" {
		t.Errorf("reasoning_effort %v", s.requests[0]["reasoning_effort"])
	}
	if _, ok := s.requests[0]["max_completion_tokens"]; ok {
		t.Error("max_completion_tokens sent for MaxTokens 0")
	}
	if _, ok := s.requests[0]["tools"]; ok {
		t.Error("empty tools sent")
	}
	_, err = agent.Run(context.Background(), p, nil, agent.Request{Settings: settings, User: "Q"})
	var refusal *agent.RefusalError
	if !errors.As(err, &refusal) || refusal.Category != "content_filter" {
		t.Errorf("content_filter: %v", err)
	}
}

func TestOpenAIBadRequestIsFailure(t *testing.T) {
	s := &chatServer{t: t, status: http.StatusBadRequest}
	p, settings := openAI(t, s, agent.Settings{Model: "m", MaxTurns: 3})
	res, err := agent.Run(context.Background(), p, &caller{}, agent.Request{Settings: settings, User: "Q", Tools: tools})
	if err == nil || !strings.Contains(err.Error(), "tools are not supported by this model") {
		t.Errorf("400 not surfaced with the server's message: %v", err)
	}
	if res.Turns != 0 || len(s.requests) != 1 {
		t.Errorf("turns %d, requests %d (a 400 is not retried)", res.Turns, len(s.requests))
	}
}
