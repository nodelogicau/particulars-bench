// Package judge grades answers with LongMemEval's per-type prompts.
package judge

import (
	"context"
	"strings"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/dataset"
	"github.com/nodelogicau/particulars-bench/prompts"
)

// Verdict is one grading.
type Verdict struct {
	Correct bool        `json:"correct"`
	Prompt  string      `json:"prompt"`
	Raw     string      `json:"raw"`
	Usage   agent.Usage `json:"usage"`
}

// Render fills the question type's template.
func Render(q dataset.Question, response string) (name, prompt string) {
	name, tmpl := prompts.Judge(q.Type, q.Abstention())
	return name, strings.NewReplacer("{question}", q.Question, "{answer}", q.Answer, "{response}", response).Replace(tmpl)
}

// Parse reads a verdict as LongMemEval does: correct when the output
// contains "yes", case-insensitively.
func Parse(raw string) bool { return strings.Contains(strings.ToLower(raw), "yes") }

// Grade asks the judge model for a verdict.
func Grade(ctx context.Context, m agent.Model, s agent.Settings, q dataset.Question, response string) (Verdict, error) {
	name, prompt := Render(q, response)
	raw, usage, err := agent.Complete(ctx, m, s, "", prompt)
	return Verdict{Correct: Parse(raw), Prompt: name, Raw: raw, Usage: usage}, err
}
