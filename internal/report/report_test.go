package report_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/answer"
	"github.com/nodelogicau/particulars-bench/internal/dataset"
	"github.com/nodelogicau/particulars-bench/internal/judge"
	"github.com/nodelogicau/particulars-bench/internal/report"
	"github.com/nodelogicau/particulars-bench/internal/run"
	"github.com/nodelogicau/particulars-bench/internal/testutil"
)

func TestWilson(t *testing.T) {
	// Reference values: 7/10 → [0.3968, 0.8922]; 0/10 → [0, 0.2775].
	a := report.Wilson(7, 10)
	if math.Abs(a.Low-0.3968) > 1e-3 || math.Abs(a.High-0.8922) > 1e-3 || a.Accuracy != 0.7 {
		t.Errorf("7/10: %+v", a)
	}
	z := report.Wilson(0, 10)
	if z.Low != 0 || math.Abs(z.High-0.2775) > 1e-3 {
		t.Errorf("0/10: %+v", z)
	}
	if e := report.Wilson(0, 0); e.N != 0 || e.Accuracy != 0 {
		t.Errorf("empty: %+v", e)
	}
}

func TestBuildAndMarkdown(t *testing.T) {
	qs, err := dataset.Parse(testutil.FixtureJSON(2))
	if err != nil {
		t.Fatal(err)
	}
	d, err := run.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	meta := run.Meta{Dataset: run.Dataset{Variant: "fixture", SHA256: "abc"}, DKFFormat: "dkf/0.1"}
	for _, q := range qs {
		meta.QuestionIDs = append(meta.QuestionIDs, q.ID)
	}
	settings := agent.Settings{Model: "m", Effort: "medium"}
	labels := []string{"no-memory", "bm25", "cli", "cli-spec"}
	for _, label := range labels {
		kind := "baseline"
		if strings.HasPrefix(label, "cli") {
			kind = "implementation"
		}
		v := run.Variant{Label: label, Kind: kind, Settings: settings, Repeats: 1, SpecToolsOnly: label == "cli-spec"}
		if label == "cli" {
			v.OfferedExtensions = []string{"topics_list"}
		}
		if err := d.WriteVariant(v); err != nil {
			t.Fatal(err)
		}
		for i, q := range qs {
			a := run.Answer{Variant: label, QuestionID: q.ID, Output: answer.Output{Text: "red", Usage: agent.Usage{Calls: 1, InputTokens: 100, OutputTokens: 10}}}
			if label == "cli" {
				a.Output.ToolCalls = map[string]int{"knowledge_recall": 1, "topics_list": 1}
			}
			if label == "cli" && i == 0 {
				a.Failure = "starting server: exit status 3"
				a.Output = answer.Output{}
			}
			if err := d.Append("answers/"+label, a); err != nil {
				t.Fatal(err)
			}
			if a.Failure != "" {
				continue
			}
			j := run.Judgment{Variant: label, QuestionID: q.ID, Settings: agent.Settings{Model: "judge"}, Verdict: judge.Verdict{Correct: i%2 == 0, Raw: "yes"}}
			if err := d.Append(run.JudgmentStream("judge", label), j); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, err := report.Build(d, meta, qs, time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC), "judge")
	if err != nil {
		t.Fatal(err)
	}
	if r.Schema != "bench-report/1" || len(r.Variants) != 4 || len(r.Strata) != 5 || r.Judge == nil || r.Judge.Model != "judge" {
		t.Fatalf("report header %+v", r)
	}
	var cli report.VariantResult
	for _, v := range r.Variants {
		if v.Label == "cli" {
			cli = v
		}
	}
	if cli.Failures != 1 || cli.Accuracy[report.OverallRow].N != len(qs)-1 {
		t.Errorf("infrastructure failure should be counted and excluded: failures %d, n %d", cli.Failures, cli.Accuracy[report.OverallRow].N)
	}
	if cli.ExtensionCalls["topics_list"] != len(qs)-1 {
		t.Errorf("extension calls %v", cli.ExtensionCalls)
	}

	md := report.Markdown(r)
	header := "| Type | bm25 | cli | cli-spec | no-memory |"
	if !strings.Contains(md, header) {
		t.Errorf("variants should be columns:\n%s", md)
	}
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "| ") && (strings.Contains(line, "abstention") || strings.Contains(line, "Overall")) {
			if strings.Count(line, "n=") != 4 || strings.Count(line, "–") != 4 {
				t.Errorf("every cell needs n and an interval: %s", line)
			}
		}
	}
	for _, want := range []string{"Tool calls / answer", "`topics_list` 9", "withheld (`--spec-tools-only`)", "Infrastructure failures"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
}

func TestProvidersInReport(t *testing.T) {
	qs, err := dataset.Parse(testutil.FixtureJSON(1))
	if err != nil {
		t.Fatal(err)
	}
	d, err := run.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	meta := run.Meta{Dataset: run.Dataset{Variant: "fixture"}}
	for _, q := range qs {
		meta.QuestionIDs = append(meta.QuestionIDs, q.ID)
	}
	router := agent.Settings{Provider: "openai", BaseURL: "https://openrouter.ai/api/v1", Model: "x/y",
		ExtraBody: map[string]any{"provider": map[string]any{"order": []any{"a"}}}}
	for _, v := range []run.Variant{
		{Label: "claude", Kind: "baseline", Settings: agent.Settings{Model: "claude-opus-5-5", Effort: "medium"}},
		{Label: "routed", Kind: "baseline", Settings: router},
	} {
		if err := d.WriteVariant(v); err != nil {
			t.Fatal(err)
		}
		for i, q := range qs {
			u := agent.Usage{Calls: 1}
			if v.Label == "routed" {
				up := "A"
				if i%2 == 1 {
					up = "B"
				}
				u.PerCall = []agent.CallUsage{{Upstream: up}}
			}
			if err := d.Append("answers/"+v.Label, run.Answer{Variant: v.Label, QuestionID: q.ID, Output: answer.Output{Usage: u}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, err := report.Build(d, meta, qs, time.Now(), "judge")
	if err != nil {
		t.Fatal(err)
	}
	if !r.TokensApproximate || len(r.Roles) != 2 {
		t.Fatalf("approximate %t, roles %+v", r.TokensApproximate, r.Roles)
	}
	routed := r.Roles[1]
	if routed.Role != "answer" || routed.Settings.BaseURL != router.BaseURL || strings.Join(routed.Upstreams, ",") != "A,B" || routed.Variants[0] != "routed" {
		t.Errorf("routed role %+v", routed)
	}
	md := report.Markdown(r)
	for _, want := range []string{
		"Token figures are approximate",
		"the answer model `x/y` was served by more than one upstream (A, B)",
		"`x/y` via openai at `https://openrouter.ai/api/v1` (extra body",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
}
