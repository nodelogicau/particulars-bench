package ingest

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/dataset"
	"github.com/nodelogicau/particulars-bench/internal/impl"
	"github.com/nodelogicau/particulars-bench/internal/workspace"
	"github.com/nodelogicau/particulars-bench/prompts"
)

// PromptName is the ingest phase prompt.
const PromptName = "ingest"

// Ingester ingests questions through one implementation.
type Ingester struct {
	Cache    Cache
	Impl     impl.Impl
	Model    agent.Model
	Settings agent.Settings
	// Variant and DatasetSHA256 identify the dataset in the key.
	Variant       string
	DatasetSHA256 string
	// Probe is the implementation on an empty workspace (see ProbeImpl).
	Probe Probe
}

// Key returns the cache key for a question.
func (in *Ingester) Key(q dataset.Question) KeyInputs {
	return KeyInputs{
		DatasetVariant: in.Variant,
		DatasetSHA256:  in.DatasetSHA256,
		QuestionID:     q.ID,
		Settings:       in.Settings,
		PromptSHA256:   prompts.Hash(PromptName),
		Probe:          in.Probe,
	}
}

// Ensure returns the question's ingested workspace, from the cache when an
// intact entry exists, otherwise by ingesting it. cached reports whether no
// model call was made.
func (in *Ingester) Ensure(ctx context.Context, q dataset.Question) (m *Manifest, cached bool, err error) {
	inputs := in.Key(q)
	key := inputs.Key()
	if m, ok, err := in.Cache.Load(key); err != nil || ok {
		return m, ok, err
	}
	m, err = in.ingest(ctx, q, inputs, key)
	return m, false, err
}

func (in *Ingester) ingest(ctx context.Context, q dataset.Question, inputs KeyInputs, key string) (*Manifest, error) {
	// An entry without an intact manifest is an interrupted or damaged
	// ingest: start again from an empty workspace.
	if err := os.RemoveAll(in.Cache.Dir(key)); err != nil {
		return nil, err
	}
	dir := in.Cache.Workspace(key)
	if _, err := workspace.Create(dir); err != nil {
		return nil, err
	}
	srv, err := impl.Start(ctx, in.Impl, dir)
	if err != nil {
		return nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = srv.Close()
		}
	}()

	m := &Manifest{Key: key, Inputs: inputs}
	claimSession := map[string]string{}
	known := map[string]bool{}
	for _, s := range q.Sessions {
		res, err := agent.Run(ctx, in.Model, srv, agent.Request{
			Settings:     in.Settings,
			Instructions: srv.Instructions,
			Phase:        prompts.Get(PromptName),
			User:         SessionMessage(s),
			Tools:        srv.Tools,
		})
		m.Usage.Add(res.Usage)
		if err != nil {
			return m, fmt.Errorf("session %s: %w", s.ID, err)
		}
		rec := SessionRecord{
			ID: s.ID, Date: s.Date.Format(time.RFC3339), Turns: res.Turns, Capped: res.Capped,
			ToolCalls: res.ToolCalls, ToolErrors: res.ToolErrors,
		}
		ids, err := workspace.ObjectIDs(dir, "claims")
		if err != nil {
			return m, err
		}
		for id := range ids {
			if !known[id] {
				known[id] = true
				rec.Claims = append(rec.Claims, id)
				claimSession[id] = s.Date.UTC().Format(time.DateOnly)
			}
		}
		sort.Strings(rec.Claims)
		m.Sessions = append(m.Sessions, rec)
	}
	closed = true
	if err := srv.Close(); err != nil {
		return m, fmt.Errorf("closing server: %w", err)
	}
	if m.Metrics, err = workspace.ComputeMetrics(dir, claimSession); err != nil {
		return m, err
	}
	m.Created = time.Now().UTC()
	return m, in.Cache.Commit(m)
}

// SessionMessage renders one session for the ingest agent.
func SessionMessage(s dataset.Session) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Session %s happened at %s (%s). Use %s as the timestamp of everything you record from it.\n\n",
		s.ID, s.Date.Format(time.RFC3339), s.DateRaw, s.Date.Format(time.RFC3339))
	b.WriteString(Transcript(s))
	return b.String()
}

// Transcript renders a session's turns.
func Transcript(s dataset.Session) string {
	var b strings.Builder
	b.WriteString("<session>\n")
	for _, t := range s.Turns {
		fmt.Fprintf(&b, "%s: %s\n\n", t.Role, strings.TrimSpace(t.Content))
	}
	b.WriteString("</session>")
	return b.String()
}
