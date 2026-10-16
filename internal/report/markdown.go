package report

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/nodelogicau/particulars-bench/internal/agent"
)

// Markdown renders the report: variants as columns, question types as rows,
// every accuracy cell with n and its 95% interval, then cost, extensions,
// ingest, and every configuration that moved the numbers.
func Markdown(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# LongMemEval — %s\n\n", r.Date.Format("2006-01-02"))
	fmt.Fprintf(&b, "Dataset `%s` (`%s`, sha256 `%s`), seed %d, %d questions. DKF format written: `%s`. Harness `%s`.\n",
		r.Dataset.Variant, r.Dataset.File, short(r.Dataset.SHA256), r.Selection.Seed, len(r.QuestionIDs), r.DKFFormat, short(r.HarnessCommit))
	if r.Judge != nil {
		fmt.Fprintf(&b, "Judge %s with LongMemEval's per-type prompts.", model(*r.Judge))
	}
	b.WriteString(" Scores compare variants within this report only.\n\n")
	for _, role := range r.Roles {
		if len(role.Upstreams) > 1 {
			name := role.Role
			if role.Judge != "" {
				name += " " + role.Judge
			}
			fmt.Fprintf(&b, "> **Warning:** the %s model `%s` was served by more than one upstream (%s), so it was not served consistently.\n\n",
				name, role.Settings.Model, strings.Join(role.Upstreams, ", "))
		}
	}

	if len(r.Variants) == 0 {
		b.WriteString("No variants answered.\n")
		return b.String()
	}

	b.WriteString("## Accuracy\n\n")
	b.WriteString("Each cell: accuracy (n, Wilson 95% interval).")
	if anyRepeats(r) {
		b.WriteString(" With repeats, a question scores the mean of its answers and n counts questions.")
	}
	b.WriteString("\n\n")
	header(&b, "Type", r.Variants)
	for _, row := range append(append([]string(nil), r.Strata...), OverallRow) {
		label := row
		if row == OverallRow {
			label = "**Overall**"
		}
		cells := []string{label}
		for _, v := range r.Variants {
			cells = append(cells, cell(v.Accuracy[row]))
		}
		line(&b, cells)
	}

	b.WriteString("\n## Cost per answer\n\n")
	if r.TokensApproximate {
		b.WriteString("Token figures are approximate across variants: they used different providers, whose tokenisers differ.\n\n")
	}
	header(&b, "", r.Variants)
	rows := []struct {
		name string
		f    func(VariantResult) string
	}{
		{"Input tokens / answer", func(v VariantResult) string { return fmt.Sprintf("%.0f", v.InputPerAnswer) }},
		{"Output tokens / answer", func(v VariantResult) string { return fmt.Sprintf("%.0f", v.OutputPerAnswer) }},
		{"Tool calls / answer", func(v VariantResult) string { return fmt.Sprintf("%.1f", v.ToolCallsPerAns) }},
		{"Answers", func(v VariantResult) string { return fmt.Sprint(v.Answers) }},
		{"Infrastructure failures", func(v VariantResult) string { return fmt.Sprint(v.Failures) }},
		{"Ungraded", func(v VariantResult) string { return fmt.Sprint(v.Ungraded) }},
	}
	for _, row := range rows {
		cells := []string{row.name}
		for _, v := range r.Variants {
			cells = append(cells, row.f(v))
		}
		line(&b, cells)
	}

	b.WriteString("\n## Tool calls\n\n")
	b.WriteString("| Variant | Calls by tool | Extensions offered (calls) |\n|---|---|---|\n")
	for _, v := range r.Variants {
		if v.Kind != "implementation" {
			continue
		}
		ext := "none"
		if v.SpecToolsOnly {
			ext = "withheld (`--spec-tools-only`)"
		} else if len(v.ExtensionCalls) > 0 {
			ext = counts(v.ExtensionCalls)
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", v.Label, counts(v.ToolCalls), ext)
	}

	if len(r.OtherJudges) > 0 {
		fmt.Fprintf(&b, "\n## Judge agreement\n\nShare of answers graded by both on which each judge agrees with the primary judge `%s`.\n\n", r.JudgeLabel)
		cells := []string{"Type"}
		for _, j := range r.OtherJudges {
			cells = append(cells, j.Judge)
		}
		line(&b, cells)
		b.WriteString("|" + strings.Repeat("---|", len(cells)) + "\n")
		for _, row := range append(append([]string(nil), r.Strata...), OverallRow) {
			cells := []string{row}
			if row == OverallRow {
				cells[0] = "**Overall**"
			}
			for _, j := range r.OtherJudges {
				a := j.Agreement[row]
				if a.N == 0 {
					cells = append(cells, "— (n=0)")
				} else {
					cells = append(cells, fmt.Sprintf("%.0f%% (%d/%d)", a.Rate*100, a.Agree, a.N))
				}
			}
			line(&b, cells)
		}
		for _, j := range r.OtherJudges {
			if j.Settings != nil {
				fmt.Fprintf(&b, "\n- %s: %s", j.Judge, model(*j.Settings))
			}
		}
		b.WriteString("\n")
	}

	in := r.Ingest
	b.WriteString("\n## Ingest\n\n")
	if in.Questions == 0 {
		b.WriteString("No ingest in this run.\n")
	} else {
		onDate := "n/a"
		if in.MeanOnSessionDate != nil {
			onDate = fmt.Sprintf("%.0f%%", *in.MeanOnSessionDate*100)
		}
		fmt.Fprintf(&b, "Implementation %s: %d questions (%d from cache, %d failed). Per workspace, on average: %.1f claims, %.1f particulars, %.1f topics, %.1f syntheses; %s of claims dated on their session's date. Ingest tokens: %d in, %d out.\n",
			strings.Join(in.Impls, ", "), in.Questions, in.Cached, in.Failures, in.MeanClaims, in.MeanParticular, in.MeanTopics, in.MeanSyntheses, onDate, in.Tokens.Input, in.Tokens.Output)
	}

	b.WriteString("\n## Variants\n\n")
	for _, v := range r.Variants {
		fmt.Fprintf(&b, "- **%s** (%s): model %s, max %d turns", v.Label, v.Kind, model(v.Settings), v.Settings.MaxTurns)
		if v.Command != "" {
			fmt.Fprintf(&b, "; `%s`", v.Command)
		}
		if v.ServerInfo.Name != "" {
			fmt.Fprintf(&b, "; server %s %s", v.ServerInfo.Name, v.ServerInfo.Version)
		}
		if v.Kind == "implementation" {
			fmt.Fprintf(&b, "; spec tools only: %t", v.SpecToolsOnly)
		}
		if v.BM25K > 0 {
			fmt.Fprintf(&b, "; k=%d", v.BM25K)
		}
		if v.Repeats > 1 {
			fmt.Fprintf(&b, "; %d repeats", v.Repeats)
		}
		var ps []string
		for name, h := range v.Prompts {
			ps = append(ps, fmt.Sprintf("%s `%s`", name, short(h)))
		}
		sort.Strings(ps)
		if len(ps) > 0 {
			fmt.Fprintf(&b, "; prompts %s", strings.Join(ps, ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// model describes a role's model: id, provider and endpoint when not the
// Anthropic default, and the parameters sent.
func model(s agent.Settings) string {
	out := fmt.Sprintf("`%s`", s.Model)
	if s.ProviderName() != agent.ProviderAnthropic || s.BaseURL != "" {
		out += fmt.Sprintf(" via %s", s.ProviderName())
		if s.BaseURL != "" {
			out += fmt.Sprintf(" at `%s`", s.BaseURL)
		}
	}
	var params []string
	if s.Effort != "" {
		params = append(params, "effort "+s.Effort)
	}
	if s.Temperature != nil {
		params = append(params, fmt.Sprintf("temperature %g", *s.Temperature))
	}
	if len(s.ExtraBody) > 0 {
		eb, _ := json.Marshal(s.ExtraBody)
		params = append(params, fmt.Sprintf("extra body `%s`", eb))
	}
	if len(params) > 0 {
		out += " (" + strings.Join(params, ", ") + ")"
	}
	return out
}

func anyRepeats(r *Report) bool {
	for _, v := range r.Variants {
		if v.Repeats > 1 {
			return true
		}
	}
	return false
}

func cell(a Accuracy) string {
	if a.N == 0 {
		return "— (n=0)"
	}
	return fmt.Sprintf("%.0f%% (n=%d, %.0f–%.0f%%)", a.Accuracy*100, a.N, a.Low*100, a.High*100)
}

func header(b *strings.Builder, first string, vs []VariantResult) {
	cells := []string{first}
	for _, v := range vs {
		cells = append(cells, v.Label)
	}
	line(b, cells)
	b.WriteString("|" + strings.Repeat("---|", len(cells)) + "\n")
}

func line(b *strings.Builder, cells []string) {
	b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
}

func counts(m map[string]int) string {
	if len(m) == 0 {
		return "none"
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("`%s` %d", n, m[n])
	}
	return strings.Join(parts, ", ")
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
