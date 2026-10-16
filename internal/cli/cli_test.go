package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/cli"
	"github.com/nodelogicau/particulars-bench/internal/fakeserver"
	"github.com/nodelogicau/particulars-bench/internal/report"
	"github.com/nodelogicau/particulars-bench/internal/testutil"
)

func TestMain(m *testing.M) {
	fakeserver.Main()
	os.Exit(m.Run())
}

type env struct {
	stdout, stderr bytes.Buffer
	// key is ANTHROPIC_API_KEY; vars holds any other variable.
	key        string
	vars       map[string]string
	model      agent.Provider
	modelsMade int
	// made records each provider's settings and API key.
	made []made
}

type made struct {
	settings agent.Settings
	key      string
}

func (e *env) deps() cli.Deps {
	return cli.Deps{
		Stdout: &e.stdout, Stderr: &e.stderr,
		Getenv: func(k string) string {
			if k == "ANTHROPIC_API_KEY" {
				return e.key
			}
			return e.vars[k]
		},
		NewProvider: func(s agent.Settings, key string) (agent.Provider, error) {
			e.modelsMade++
			e.made = append(e.made, made{s, key})
			return e.model, nil
		},
		Now: func() time.Time { return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) },
	}
}

func (e *env) run(args ...string) int {
	e.stdout.Reset()
	e.stderr.Reset()
	return cli.Run(context.Background(), args, e.deps())
}

func empty(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	return os.IsNotExist(err) || (err == nil && len(entries) == 0)
}

func TestNoAPIKey(t *testing.T) {
	cache := testutil.InstallFixture(t, 1)
	results := filepath.Join(t.TempDir(), "results")
	for _, phase := range []string{"ingest", "answer", "judge"} {
		e := &env{}
		code := e.run(phase, "--variant", "fixture", "--results", results, "--impl", "fake="+fakeserver.Command())
		if code == 0 || !strings.Contains(e.stderr.String(), "ANTHROPIC_API_KEY") {
			t.Errorf("%s without key: exit %d, stderr %q", phase, code, e.stderr.String())
		}
		if e.modelsMade != 0 {
			t.Errorf("%s made a model client", phase)
		}
	}
	if !empty(t, results) || !empty(t, filepath.Join(cache, "longmemeval", "workspaces")) {
		t.Error("a run or workspace was created without an API key")
	}
}

func TestDryRunMakesNoCalls(t *testing.T) {
	cache := testutil.InstallFixture(t, 3)
	results := filepath.Join(t.TempDir(), "results")
	m := &testutil.Model{Script: func(anthropic.MessageNewParams) testutil.Reply {
		t.Fatal("model called in a dry run")
		return testutil.Reply{}
	}}
	for _, args := range [][]string{
		{"ingest", "--impl", "fake=" + fakeserver.Command()},
		{"answer", "--impl", "fake=" + fakeserver.Command(), "--baselines", "no-memory,bm25"},
	} {
		e := &env{model: m} // no key: a dry run needs none
		code := e.run(append(args, "--dry-run", "--variant", "fixture", "--per-type", "2", "--results", results)...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, e.stderr.String())
		}
		out := e.stdout.String()
		for _, want := range []string{"Plan (", "dataset: fixture", "10 questions", "abstention", "model calls"} {
			if !strings.Contains(out, want) {
				t.Errorf("%v: plan lacks %q:\n%s", args, want, out)
			}
		}
		if e.modelsMade != 0 || m.CallCount() != 0 {
			t.Errorf("%v: model used in dry run", args)
		}
	}
	if !empty(t, results) || !empty(t, filepath.Join(cache, "longmemeval", "workspaces")) {
		t.Error("dry run created a run or a workspace")
	}
}

var tsRE = regexp.MustCompile(`Use (\S+) as the timestamp`)

// pipelineModel plays every phase: ingest records a claim per session,
// implementations recall then answer, baselines answer, the judge says yes.
func pipelineModel() *testutil.Model {
	return &testutil.Model{Script: func(p anthropic.MessageNewParams) testutil.Reply {
		tools := testutil.ToolNames(p)
		user := p.Messages[0].Content[0].OfText.Text
		switch {
		case testutil.Has(tools, "claim_assert"):
			if len(p.Messages) > 1 {
				return testutil.Reply{Text: "recorded"}
			}
			return testutil.Reply{Tools: []testutil.ToolUse{{Name: "claim_assert", Input: map[string]any{
				"particular_id": "The user", "content": "bike", "timestamp": tsRE.FindStringSubmatch(user)[1]}}}}
		case testutil.Has(tools, "knowledge_recall"):
			if len(p.Messages) > 1 {
				return testutil.Reply{Text: "red"}
			}
			calls := []testutil.ToolUse{{Name: "knowledge_recall", Input: map[string]any{"query": "bike"}}}
			if testutil.Has(tools, "topics_list") {
				calls = append(calls, testutil.ToolUse{Name: "topics_list"})
			}
			return testutil.Reply{Tools: calls}
		case len(p.System) == 0:
			return testutil.Reply{Text: "yes"}
		default:
			return testutil.Reply{Text: "red"}
		}
	}}
}

func TestPipeline(t *testing.T) {
	testutil.InstallFixture(t, 1)
	runDir := filepath.Join(t.TempDir(), "run")
	m := pipelineModel()
	e := &env{key: "test", model: m}
	impl := "fake=" + fakeserver.Command()
	common := []string{"--variant", "fixture", "--run", runDir, "--concurrency", "2"}

	if code := e.run(append([]string{"ingest", "--impl", impl}, common...)...); code != 0 {
		t.Fatalf("ingest: %d\n%s", code, e.stderr.String())
	}
	afterIngest := m.CallCount()
	if afterIngest != 5*2*2 { // 5 questions × 2 sessions × 2 turns
		t.Errorf("ingest made %d calls", afterIngest)
	}

	// Two implementation variants answer from the same cached workspaces:
	// the answer phase's ingest makes no model calls.
	args := append([]string{"answer", "--impl", impl, "--impl", "fake2=" + fakeserver.Command("FAKE_VERSION=2.0.0"),
		"--ingest-impl", impl, "--baselines", "no-memory,full-context,bm25"}, common...)
	if code := e.run(args...); code != 0 {
		t.Fatalf("answer: %d\n%s", code, e.stderr.String())
	}
	if strings.Contains(e.stderr.String(), ": ingested") || strings.Count(e.stderr.String(), ": cached") != 5 {
		t.Errorf("answer phase re-ingested:\n%s", e.stderr.String())
	}
	if got := m.CallCount() - afterIngest; got != 5*2*2+5*3 { // 2 impls × 2 turns + 3 baselines × 1
		t.Errorf("answer made %d calls", got)
	}
	if code := e.run(append([]string{"answer", "--impl", "fake-spec=" + fakeserver.Command(), "--spec-tools-only"}, common...)...); code != 0 {
		t.Fatalf("answer --spec-tools-only: %d\n%s", code, e.stderr.String())
	}

	if code := e.run(append([]string{"judge"}, common...)...); code != 0 {
		t.Fatalf("judge: %d\n%s", code, e.stderr.String())
	}
	// A second judge on another provider writes beside the first.
	e.vars = map[string]string{"OPENAI_API_KEY": "sk"}
	e.made = nil
	if code := e.run(append([]string{"judge", "--judge-provider", "openai", "--judge-model", "gpt-4o"}, common...)...); code != 0 {
		t.Fatalf("second judge: %d\n%s", code, e.stderr.String())
	}
	js := e.made[0].settings
	if js.Provider != "openai" || js.Effort != "" || js.Temperature == nil || *js.Temperature != 0 || e.made[0].key != "sk" {
		t.Errorf("openai judge settings %+v", js)
	}
	for _, judge := range []string{"claude-opus-5-5", "gpt-4o"} {
		if _, err := os.Stat(filepath.Join(runDir, "judgments", judge, "fake.jsonl")); err != nil {
			t.Errorf("judge %s verdicts: %v", judge, err)
		}
	}

	if code := e.run(append([]string{"report"}, common...)...); code != 0 {
		t.Fatalf("report: %d\n%s", code, e.stderr.String())
	}
	md := e.stdout.String()
	if !strings.Contains(md, "| Type | bm25 | fake | fake-spec | fake2 | full-context | no-memory |") {
		t.Errorf("markdown columns:\n%s", md)
	}

	b, err := os.ReadFile(filepath.Join(runDir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var r report.Report
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.Schema != "bench-report/1" || r.DKFFormat != "dkf/0.1" || len(r.QuestionIDs) != 5 || r.Ingest.Questions != 5 {
		t.Errorf("report header: schema %s dkf %s questions %d ingest %+v", r.Schema, r.DKFFormat, len(r.QuestionIDs), r.Ingest)
	}
	if r.JudgeLabel != "claude-opus-5-5" || len(r.OtherJudges) != 1 || r.OtherJudges[0].Judge != "gpt-4o" ||
		r.OtherJudges[0].Agreement[report.OverallRow] != (report.Agreement{N: 30, Agree: 30, Rate: 1}) {
		t.Errorf("judges: primary %q, others %+v", r.JudgeLabel, r.OtherJudges)
	}
	if r.Ingest.MeanOnSessionDate == nil || *r.Ingest.MeanOnSessionDate != 1 {
		t.Errorf("ingest metrics %+v", r.Ingest)
	}
	for _, v := range r.Variants {
		acc := v.Accuracy[report.OverallRow]
		if acc.N != 5 || acc.Accuracy != 1 || v.Failures != 0 {
			t.Errorf("%s: overall %+v failures %d", v.Label, acc, v.Failures)
		}
		switch v.Label {
		case "fake":
			if v.ServerInfo.Name != "fake-dkf" || len(v.OfferedExtensions) != 1 || v.ExtensionCalls["topics_list"] != 5 || v.SpecToolsOnly {
				t.Errorf("fake variant %+v", v)
			}
		case "fake2":
			if v.ServerInfo.Version != "2.0.0" {
				t.Errorf("fake2 serverInfo %+v", v.ServerInfo)
			}
		case "fake-spec":
			if !v.SpecToolsOnly || len(v.OfferedExtensions) != 0 || v.ToolCalls["topics_list"] != 0 {
				t.Errorf("fake-spec variant %+v", v)
			}
		case "bm25":
			if v.BM25K != 3 {
				t.Errorf("bm25 k %d", v.BM25K)
			}
		}
	}
	q := r.Questions[0]
	if q.Ingest == nil || q.Outcomes["fake"][0].Tokens["ingest"].Input == 0 || q.Outcomes["fake"][0].Tokens["judge"].Input == 0 {
		t.Errorf("per-question tokens by phase missing: %+v", q.Outcomes["fake"])
	}
}

func TestProviderKeys(t *testing.T) {
	testutil.InstallFixture(t, 1)
	results := filepath.Join(t.TempDir(), "results")
	common := []string{"--variant", "fixture", "--results", results}

	// An openai judge needs OPENAI_API_KEY even when ANTHROPIC_API_KEY is set.
	e := &env{key: "anthropic"}
	code := e.run(append([]string{"judge", "--judge-provider", "openai", "--judge-model", "gpt-4o"}, common...)...)
	if code == 0 || !strings.Contains(e.stderr.String(), "OPENAI_API_KEY is not set") || e.modelsMade != 0 {
		t.Errorf("openai judge without OPENAI_API_KEY: exit %d, stderr %q", code, e.stderr.String())
	}

	// A named router variable replaces OPENAI_API_KEY and is what is sent.
	e = &env{vars: map[string]string{"OPENROUTER_API_KEY": "sk-or"}, model: pipelineModel()}
	code = e.run(append([]string{"answer", "--baselines", "no-memory", "--provider", "openai",
		"--base-url", "https://openrouter.ai/api/v1/", "--api-key-env", "OPENROUTER_API_KEY", "--model", "x/y",
		"--extra-body", `{"provider":{"order":["x"],"allow_fallbacks":false}}`}, common...)...)
	if code != 0 {
		t.Fatalf("answer through a router: exit %d\n%s", code, e.stderr.String())
	}
	s := e.made[0].settings
	if e.made[0].key != "sk-or" || s.Provider != "openai" || s.BaseURL != "https://openrouter.ai/api/v1" || s.Effort != "" || s.ExtraBody["provider"] == nil {
		t.Errorf("router provider made with %+v, key %q", s, e.made[0].key)
	}
	if !strings.Contains(e.stdout.String(), "agent model: openai x/y at https://openrouter.ai/api/v1, key OPENROUTER_API_KEY") {
		t.Errorf("plan lacks the role:\n%s", e.stdout.String())
	}

	// Anthropic-only settings are as before providers existed.
	e = &env{key: "k", model: pipelineModel()}
	if code := e.run(append([]string{"answer", "--baselines", "no-memory"}, common...)...); code != 0 {
		t.Fatalf("anthropic answer: %d\n%s", code, e.stderr.String())
	}
	want := agent.Settings{Model: "claude-opus-5-5", Effort: "medium", MaxTokens: 16000, MaxTurns: 30}
	if !reflect.DeepEqual(e.made[0].settings, want) || e.made[0].key != "k" {
		t.Errorf("anthropic settings %+v", e.made[0].settings)
	}
}

func TestProviderFlagValidation(t *testing.T) {
	testutil.InstallFixture(t, 1)
	results := filepath.Join(t.TempDir(), "results")
	for _, bad := range [][]string{
		{"--provider", "gemini"},
		{"--extra-body", "[1]"},
		{"--judge-extra-body", "{"},
		{"--provider", "openai", "--effort", "turbo"},
		{"--effort", "minimal"}, // an OpenAI effort, not an Anthropic one
		{"--judge-label", "a/b"},
	} {
		e := &env{}
		if code := e.run(append([]string{"answer", "--dry-run", "--baselines", "no-memory", "--variant", "fixture", "--results", results}, bad...)...); code != 2 {
			t.Errorf("%v: exit %d, want 2 (%s)", bad, code, e.stderr.String())
		}
	}
}
