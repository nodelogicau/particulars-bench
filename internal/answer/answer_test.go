package answer_test

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/answer"
	"github.com/nodelogicau/particulars-bench/internal/dataset"
	"github.com/nodelogicau/particulars-bench/internal/fakeserver"
	"github.com/nodelogicau/particulars-bench/internal/impl"
	"github.com/nodelogicau/particulars-bench/internal/ingest"
	"github.com/nodelogicau/particulars-bench/internal/testutil"
	"github.com/nodelogicau/particulars-bench/internal/workspace"
)

func TestMain(m *testing.M) {
	fakeserver.Main()
	os.Exit(m.Run())
}

var settings = agent.Settings{Model: "m", Effort: "medium", MaxTokens: 1000, MaxTurns: 5}

func TestBM25(t *testing.T) {
	docs := []string{"the cat sat on the mat", "my bike is red", "dogs and cats", "I sold my red bike last week"}
	got := answer.BM25TopK(docs, "What colour is my bike?", 2)
	if len(got) != 2 || (got[0] != 1 && got[0] != 3) || (got[1] != 1 && got[1] != 3) {
		t.Errorf("top 2 for bike: %v", got)
	}
	if got := answer.BM25TopK(docs, "zebra", 3); len(got) != 0 {
		t.Errorf("no shared term, got %v", got)
	}
}

func fixtureQuestion(t *testing.T) dataset.Question {
	qs, err := dataset.Parse(testutil.FixtureJSON(1))
	if err != nil {
		t.Fatal(err)
	}
	return qs[0]
}

func TestBaselines(t *testing.T) {
	q := fixtureQuestion(t)
	var users []string
	m := &testutil.Model{Script: func(p anthropic.MessageNewParams) testutil.Reply {
		users = append(users, p.Messages[0].Content[0].OfText.Text)
		if len(p.Tools) != 0 {
			t.Error("baseline offered tools")
		}
		return testutil.Reply{Text: "red"}
	}}
	for _, b := range answer.BaselineNames {
		out, err := answer.Baseline(context.Background(), m, settings, b, 1, q)
		if err != nil || out.Text != "red" {
			t.Fatalf("%s: %q %v", b, out.Text, err)
		}
		if b == answer.BM25 && !reflect.DeepEqual(out.Retrieved, []string{q.ID + "-later"}) {
			t.Errorf("bm25 k=1 retrieved %v", out.Retrieved)
		}
	}
	if strings.Contains(users[0], "<session>") {
		t.Error("no-memory baseline saw history")
	}
	if strings.Count(users[1], "<session>") != 2 || strings.Index(users[1], "-earlier") > strings.Index(users[1], "-later") {
		t.Error("full-context baseline should hold every session, oldest first")
	}
	if strings.Count(users[2], "<session>") != 1 {
		t.Error("bm25 k=1 should hold one session")
	}
}

func TestWithMemoryIsReadOnlyAndLeavesCacheIntact(t *testing.T) {
	q := fixtureQuestion(t)
	cache := ingest.Cache{Root: t.TempDir()}
	key := "k"
	ws := cache.Workspace(key)
	if _, err := workspace.Create(ws); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ws+"/claims", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ws+"/claims/clm_1.yaml", []byte("id: clm_1\ntype: claim\ncontent: The bike is red.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := workspace.Hash(ws)
	if err != nil {
		t.Fatal(err)
	}
	im := impl.Impl{Label: "fake", Command: fakeserver.Command()}

	for _, specOnly := range []bool{false, true} {
		var offered []string
		m := &testutil.Model{Script: func(p anthropic.MessageNewParams) testutil.Reply {
			offered = testutil.ToolNames(p)
			if len(p.Messages) == 1 {
				return testutil.Reply{Tools: []testutil.ToolUse{{Name: "knowledge_recall", Input: map[string]any{"query": "bike"}}}}
			}
			return testutil.Reply{Text: "red"}
		}}
		out, info, ext, err := answer.WithMemory(context.Background(), m, settings, im, specOnly, cache, key, t.TempDir(), q)
		if err != nil {
			t.Fatal(err)
		}
		if out.Text != "red" || out.ToolCalls["knowledge_recall"] != 1 || info.Name != "fake-dkf" {
			t.Errorf("answer %+v info %+v", out, info)
		}
		for _, w := range []string{"claim_assert", "particular_define", "note_write"} {
			if testutil.Has(offered, w) {
				t.Errorf("write tool %s offered", w)
			}
		}
		if specOnly == testutil.Has(offered, "topics_list") {
			t.Errorf("spec-tools-only=%t, topics_list offered=%t", specOnly, testutil.Has(offered, "topics_list"))
		}
		if !specOnly && !reflect.DeepEqual(ext, []string{"topics_list"}) {
			t.Errorf("extensions %v", ext)
		}
		if ok, err := workspace.Matches(ws, files); !ok || err != nil {
			t.Fatalf("cached workspace changed by the answer phase: %v", err)
		}
	}
}
