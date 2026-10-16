package judge_test

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/dataset"
	"github.com/nodelogicau/particulars-bench/internal/judge"
	"github.com/nodelogicau/particulars-bench/internal/testutil"
)

func TestPromptPerType(t *testing.T) {
	cases := []struct {
		id, typ, want string
	}{
		{"q1", "single-session-user", "judge-default"},
		{"q2", "multi-session", "judge-default"},
		{"q3", "temporal-reasoning", "judge-temporal-reasoning"},
		{"q4", "knowledge-update", "judge-knowledge-update"},
		{"q5", "single-session-preference", "judge-single-session-preference"},
		{"q6_abs", "temporal-reasoning", "judge-abstention"},
	}
	for _, c := range cases {
		name, prompt := judge.Render(dataset.Question{ID: c.id, Type: c.typ, Question: "Q?", Answer: "A"}, "R")
		if name != c.want {
			t.Errorf("%s/%s: prompt %s, want %s", c.id, c.typ, name, c.want)
		}
		if !strings.Contains(prompt, "Q?") || !strings.Contains(prompt, ": A\n") || !strings.Contains(prompt, "Model Response: R") {
			t.Errorf("%s: template not filled:\n%s", c.id, prompt)
		}
	}
}

func TestAbstentionGradedAsAbstention(t *testing.T) {
	q := dataset.Question{ID: "x_abs", Type: "multi-session", Question: "What is my cat's name?", Answer: "The user never mentioned a cat."}
	m := &testutil.Model{Script: func(p anthropic.MessageNewParams) testutil.Reply {
		prompt := p.Messages[0].Content[0].OfText.Text
		if !strings.Contains(prompt, "unanswerable") || len(p.System) != 0 {
			t.Errorf("abstention prompt not used:\n%s", prompt)
		}
		return testutil.Reply{Text: "Yes."}
	}}
	v, err := judge.Grade(context.Background(), m, agent.Settings{Model: "j", MaxTokens: 100, MaxTurns: 1}, q, "You never told me about a cat.")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Correct || v.Prompt != "judge-abstention" || v.Raw != "Yes." {
		t.Errorf("verdict %+v", v)
	}
}

func TestParse(t *testing.T) {
	for raw, want := range map[string]bool{"yes": true, "Yes.": true, "no": false, "No, it is not.": false} {
		if judge.Parse(raw) != want {
			t.Errorf("Parse(%q) != %t", raw, want)
		}
	}
}
