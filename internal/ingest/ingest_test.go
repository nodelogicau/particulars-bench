package ingest_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/dataset"
	"github.com/nodelogicau/particulars-bench/internal/fakeserver"
	"github.com/nodelogicau/particulars-bench/internal/impl"
	"github.com/nodelogicau/particulars-bench/internal/ingest"
	"github.com/nodelogicau/particulars-bench/internal/testutil"
)

func TestMain(m *testing.M) {
	fakeserver.Main()
	os.Exit(m.Run())
}

var tsRE = regexp.MustCompile(`Use (\S+) as the timestamp`)

// IngestModel records one claim per session, timestamped as instructed.
func IngestModel() *testutil.Model {
	return &testutil.Model{Script: func(p anthropic.MessageNewParams) testutil.Reply {
		if len(p.Messages) > 1 {
			return testutil.Reply{Text: "recorded"}
		}
		user := p.Messages[0].Content[0].OfText.Text
		ts := tsRE.FindStringSubmatch(user)[1]
		return testutil.Reply{Tools: []testutil.ToolUse{
			{Name: "particular_define", Input: map[string]any{"label": "The user"}},
			{Name: "claim_assert", Input: map[string]any{"particular_id": "The user", "content": user[strings.Index(user, "<session>"):], "timestamp": ts, "topics": []string{"bikes"}}},
		}}
	}}
}

func question(t *testing.T) dataset.Question {
	qs, err := dataset.Parse(testutil.FixtureJSON(1))
	if err != nil {
		t.Fatal(err)
	}
	return qs[0]
}

func ingester(t *testing.T, m agent.Model, env ...string) *ingest.Ingester {
	t.Helper()
	im := impl.Impl{Label: "fake", Command: fakeserver.Command(env...)}
	probe, err := ingest.ProbeImpl(context.Background(), im, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &ingest.Ingester{
		Cache: ingest.Cache{Root: t.TempDir()}, Impl: im, Model: m,
		Settings: agent.Settings{Model: "m", Effort: "medium", MaxTokens: 1000, MaxTurns: 5},
		Variant:  "fixture", DatasetSHA256: "abc", Probe: probe,
	}
}

func TestProbeIsWorkspaceIndependent(t *testing.T) {
	im := impl.Impl{Label: "fake", Command: fakeserver.Command()}
	a, err := ingest.ProbeImpl(context.Background(), im, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := ingest.ProbeImpl(context.Background(), im, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if a.Instructions != b.Instructions || string(a.Tools) != string(b.Tools) {
		t.Errorf("probes of two empty workspaces differ:\n%s\n%s", a.Instructions, b.Instructions)
	}
	if !strings.Contains(a.Instructions, "{workspace}") || !strings.Contains(a.Instructions, "{workspace-id}") {
		t.Errorf("path and id not normalised: %s", a.Instructions)
	}
}

func TestGuidanceChangeInvalidates(t *testing.T) {
	q := question(t)
	base := ingester(t, nil).Key(q).Key()
	if again := ingester(t, nil).Key(q).Key(); again != base {
		t.Error("identical inputs gave different keys")
	}
	if k := ingester(t, nil, "FAKE_GUIDANCE=Record_more").Key(q).Key(); k == base {
		t.Error("changed initialize instructions did not change the key")
	}
	if k := ingester(t, nil, "FAKE_VERSION=9.9.9").Key(q).Key(); k == base {
		t.Error("changed serverInfo did not change the key")
	}
	in := ingester(t, nil)
	in.Settings.Model = "other"
	if in.Key(q).Key() == base {
		t.Error("changed ingest model did not change the key")
	}
	inputs := ingester(t, nil).Key(q)
	inputs.Probe.Tools = []byte(strings.Replace(string(inputs.Probe.Tools), "Recall claims.", "Recall claims, better.", 1))
	if inputs.Key() == base {
		t.Error("changed tool description did not change the key")
	}
}

func TestIngestAndReuse(t *testing.T) {
	q := question(t)
	m := IngestModel()
	in := ingester(t, m)
	man, cached, err := in.Ensure(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if cached || m.CallCount() != 4 {
		t.Fatalf("cached %t, %d model calls (want 2 sessions × 2)", cached, m.CallCount())
	}
	// Oldest session first.
	first := m.Calls[0].Messages[0].Content[0].OfText.Text
	if !strings.Contains(first, q.ID+"-earlier") || !strings.Contains(first, "2023-04-10T09:30:00Z") {
		t.Errorf("first replayed session: %s", first)
	}
	if man.Metrics.Claims != 2 || man.Metrics.OnSessionDate == nil || *man.Metrics.OnSessionDate != 1 {
		t.Errorf("metrics %+v", man.Metrics)
	}
	if len(man.Sessions) != 2 || len(man.Sessions[0].Claims) != 1 || man.Usage.Calls != 4 {
		t.Errorf("manifest sessions %+v usage %+v", man.Sessions, man.Usage)
	}

	again, cached, err := in.Ensure(context.Background(), q)
	if err != nil || !cached || again.Key != man.Key || m.CallCount() != 4 {
		t.Fatalf("second Ensure: cached %t err %v calls %d", cached, err, m.CallCount())
	}
}

func TestInterruptedIngestIsRedone(t *testing.T) {
	q := question(t)
	m := IngestModel()
	in := ingester(t, m)
	man, _, err := in.Ensure(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	// An ingest that stopped before its manifest was written.
	if err := os.Remove(filepath.Join(in.Cache.Dir(man.Key), "manifest.json")); err != nil {
		t.Fatal(err)
	}
	_, cached, err := in.Ensure(context.Background(), q)
	if err != nil || cached || m.CallCount() != 8 {
		t.Fatalf("interrupted ingest reused: cached %t calls %d err %v", cached, m.CallCount(), err)
	}
	// A cached workspace that no longer matches its manifest.
	if err := os.WriteFile(filepath.Join(in.Cache.Workspace(man.Key), "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, cached, err = in.Ensure(context.Background(), q)
	if err != nil || cached || m.CallCount() != 12 {
		t.Fatalf("altered workspace reused: cached %t calls %d err %v", cached, m.CallCount(), err)
	}
}
