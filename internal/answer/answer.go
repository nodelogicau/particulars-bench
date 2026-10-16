// Package answer answers questions: with an implementation serving a copy of
// a cached workspace through read-only tools, or with a baseline that starts
// no implementation.
package answer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/dataset"
	"github.com/nodelogicau/particulars-bench/internal/impl"
	"github.com/nodelogicau/particulars-bench/internal/ingest"
	"github.com/nodelogicau/particulars-bench/internal/workspace"
	"github.com/nodelogicau/particulars-bench/prompts"
)

// Prompt names.
const (
	MemoryPrompt   = "answer-memory"
	BaselinePrompt = "answer-baseline"
)

// Baselines, by label.
const (
	NoMemory    = "no-memory"
	FullContext = "full-context"
	BM25        = "bm25"
)

// BaselineNames lists the baselines.
var BaselineNames = []string{NoMemory, FullContext, BM25}

// Output is one answer.
type Output struct {
	Text      string         `json:"text"`
	Turns     int            `json:"turns"`
	Capped    bool           `json:"capped,omitempty"`
	ToolCalls map[string]int `json:"tool_calls,omitempty"`
	Usage     agent.Usage    `json:"usage"`
	// Retrieved lists the session ids a retrieval baseline put in context.
	Retrieved []string `json:"retrieved,omitempty"`
}

// Question renders the question as the answering model receives it.
func Question(q dataset.Question) string {
	return fmt.Sprintf("Current date: %s (%s)\n\nQuestion: %s", q.Date.Format(time.RFC3339), q.DateRaw, q.Question)
}

// WithMemory serves a copy of the cached workspace for key with im and lets
// the model answer through the read-only tools. The cached workspace itself
// is never served.
func WithMemory(ctx context.Context, m agent.Model, s agent.Settings, im impl.Impl, specOnly bool,
	cache ingest.Cache, key, tmpRoot string, q dataset.Question) (Output, impl.ServerInfo, []string, error) {
	tmp, err := os.MkdirTemp(tmpRoot, "answer-")
	if err != nil {
		return Output{}, impl.ServerInfo{}, nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	dir := filepath.Join(tmp, "workspace")
	if err := workspace.Copy(cache.Workspace(key), dir); err != nil {
		return Output{}, impl.ServerInfo{}, nil, err
	}
	srv, err := impl.Start(ctx, im, dir)
	if err != nil {
		return Output{}, impl.ServerInfo{}, nil, err
	}
	defer func() { _ = srv.Close() }()
	tools, extensions := impl.AnswerTools(srv.Tools, specOnly)
	res, err := agent.Run(ctx, m, srv, agent.Request{
		Settings:     s,
		Instructions: srv.Instructions,
		Phase:        prompts.Get(MemoryPrompt),
		User:         Question(q),
		Tools:        tools,
	})
	return Output{Text: res.Text, Turns: res.Turns, Capped: res.Capped, ToolCalls: res.ToolCalls, Usage: res.Usage},
		srv.Info, extensions, err
}

// Baseline answers with one of the baselines; bm25K is the number of
// sessions BM25 retrieves.
func Baseline(ctx context.Context, m agent.Model, s agent.Settings, name string, bm25K int, q dataset.Question) (Output, error) {
	var sessions []dataset.Session
	switch name {
	case NoMemory:
	case FullContext:
		sessions = q.Sessions
	case BM25:
		docs := make([]string, len(q.Sessions))
		for i, sess := range q.Sessions {
			docs[i] = ingest.Transcript(sess)
		}
		idx := BM25TopK(docs, q.Question, bm25K)
		sort.Ints(idx) // present retrieved sessions chronologically
		for _, i := range idx {
			sessions = append(sessions, q.Sessions[i])
		}
	default:
		return Output{}, fmt.Errorf("unknown baseline %q (known: %v)", name, BaselineNames)
	}
	var b strings.Builder
	var retrieved []string
	if len(sessions) == 0 {
		b.WriteString("There are no past sessions.\n\n")
	}
	for _, sess := range sessions {
		retrieved = append(retrieved, sess.ID)
		fmt.Fprintf(&b, "Session %s, %s (%s):\n%s\n\n", sess.ID, sess.Date.Format(time.RFC3339), sess.DateRaw, ingest.Transcript(sess))
	}
	b.WriteString(Question(q))
	text, usage, err := agent.Complete(ctx, m, s, prompts.Get(BaselinePrompt), b.String())
	out := Output{Text: text, Turns: usage.Calls, Usage: usage}
	if name == BM25 {
		out.Retrieved = retrieved
	}
	return out, err
}
