// Package report builds the bench-report/1 JSON and its Markdown rendering
// from a run directory.
package report

import (
	"math"
	"sort"
	"time"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/dataset"
	"github.com/nodelogicau/particulars-bench/internal/run"
	"github.com/nodelogicau/particulars-bench/prompts"
)

// Schema names the report format.
const Schema = "bench-report/1"

// OverallRow is the key of the all-questions accuracy.
const OverallRow = "overall"

// Accuracy is a proportion with its sample size and Wilson 95% interval.
// With repeats, each question contributes the mean of its graded answers,
// and N counts questions, not answers.
type Accuracy struct {
	N        int     `json:"n"`
	Correct  float64 `json:"correct"`
	Accuracy float64 `json:"accuracy"`
	Low      float64 `json:"ci95_low"`
	High     float64 `json:"ci95_high"`
}

// Wilson returns the accuracy of correct out of n with its Wilson score
// interval at 95%.
func Wilson(correct float64, n int) Accuracy {
	a := Accuracy{N: n, Correct: correct}
	if n == 0 {
		return a
	}
	const z = 1.959963984540054
	nf := float64(n)
	p := correct / nf
	denom := 1 + z*z/nf
	center := (p + z*z/(2*nf)) / denom
	half := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf)) / denom
	a.Accuracy = p
	a.Low = math.Max(0, center-half)
	a.High = math.Min(1, center+half)
	return a
}

// PhaseTokens is a phase's token use.
type PhaseTokens struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
}

func tokens(u agent.Usage) PhaseTokens {
	return PhaseTokens{Input: u.InputTokens, Output: u.OutputTokens}
}

// Outcome is one answer and its grading.
type Outcome struct {
	Repeat    int                    `json:"repeat"`
	Answer    string                 `json:"answer"`
	Correct   *bool                  `json:"correct,omitempty"`
	JudgeRaw  string                 `json:"judge_raw,omitempty"`
	Tokens    map[string]PhaseTokens `json:"tokens"`
	ToolCalls map[string]int         `json:"tool_calls,omitempty"`
	Retrieved []string               `json:"retrieved,omitempty"`
	Failure   string                 `json:"failure,omitempty"`
}

// Question is one question's results across variants.
type Question struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Stratum  string               `json:"stratum"`
	Ingest   *run.IngestRecord    `json:"ingest,omitempty"`
	Outcomes map[string][]Outcome `json:"outcomes"`
}

// VariantResult is one variant's description and aggregate results.
type VariantResult struct {
	run.Variant
	Accuracy map[string]Accuracy `json:"accuracy"`
	// Answers counts answers; Failures counts infrastructure failures
	// (answer or judge), which are excluded from accuracy.
	Answers           int            `json:"answers"`
	Failures          int            `json:"infrastructure_failures"`
	Ungraded          int            `json:"ungraded"`
	InputPerAnswer    float64        `json:"input_tokens_per_answer"`
	OutputPerAnswer   float64        `json:"output_tokens_per_answer"`
	ToolCallsPerAns   float64        `json:"tool_calls_per_answer"`
	ToolCalls         map[string]int `json:"tool_calls"`
	ExtensionCalls    map[string]int `json:"extension_calls"`
	JudgeTokens       PhaseTokens    `json:"judge_tokens"`
	AnswerTokensTotal PhaseTokens    `json:"answer_tokens"`
}

// IngestSummary aggregates the ingest records.
type IngestSummary struct {
	Impls          []string `json:"impls"`
	Questions      int      `json:"questions"`
	Cached         int      `json:"cached"`
	Failures       int      `json:"infrastructure_failures"`
	MeanClaims     float64  `json:"mean_claims"`
	MeanParticular float64  `json:"mean_particulars"`
	MeanTopics     float64  `json:"mean_topics"`
	MeanSyntheses  float64  `json:"mean_syntheses"`
	// MeanOnSessionDate is the mean, over questions with attributable
	// claims, of the fraction of claims dated on their session's date.
	MeanOnSessionDate *float64    `json:"mean_on_session_date"`
	Tokens            PhaseTokens `json:"tokens"`
}

// Report is bench-report/1.
type Report struct {
	Schema        string            `json:"schema"`
	Date          time.Time         `json:"date"`
	HarnessCommit string            `json:"harness_commit"`
	DKFFormat     string            `json:"dkf_format"`
	Dataset       run.Dataset       `json:"dataset"`
	Selection     dataset.Selection `json:"selection"`
	QuestionIDs   []string          `json:"question_ids"`
	Strata        []string          `json:"strata"`
	Judge         *agent.Settings   `json:"judge,omitempty"`
	JudgePrompts  map[string]string `json:"judge_prompt_sha256"`
	Ingest        IngestSummary     `json:"ingest"`
	Variants      []VariantResult   `json:"variants"`
	Questions     []Question        `json:"questions"`
}

// Build assembles the report for a run.
func Build(d *run.Dir, meta run.Meta, qs []dataset.Question, now time.Time) (*Report, error) {
	r := &Report{
		Schema: Schema, Date: now.UTC(), HarnessCommit: meta.HarnessCommit, DKFFormat: meta.DKFFormat,
		Dataset: meta.Dataset, Selection: meta.Selection, QuestionIDs: meta.QuestionIDs,
		JudgePrompts: map[string]string{},
	}
	for _, n := range []string{"judge-default", "judge-temporal-reasoning", "judge-knowledge-update", "judge-single-session-preference", "judge-abstention"} {
		r.JudgePrompts[n] = prompts.Hash(n)
	}

	byID := map[string]*Question{}
	strata := map[string]bool{}
	for _, q := range qs {
		rq := &Question{ID: q.ID, Type: q.Type, Stratum: q.Stratum(), Outcomes: map[string][]Outcome{}}
		byID[q.ID] = rq
		strata[q.Stratum()] = true
	}
	for s := range strata {
		r.Strata = append(r.Strata, s)
	}
	sort.Strings(r.Strata)

	ingests, err := run.Read[run.IngestRecord](d, "ingest")
	if err != nil {
		return nil, err
	}
	r.Ingest = summariseIngest(ingests, byID)

	variants, err := d.Variants()
	if err != nil {
		return nil, err
	}
	for _, v := range variants {
		vr, err := buildVariant(d, v, byID, r)
		if err != nil {
			return nil, err
		}
		r.Variants = append(r.Variants, vr)
	}
	for _, q := range qs {
		r.Questions = append(r.Questions, *byID[q.ID])
	}
	return r, nil
}

func summariseIngest(recs []run.IngestRecord, byID map[string]*Question) IngestSummary {
	var s IngestSummary
	impls := map[string]bool{}
	var onDate []float64
	n := 0
	// A later record for a question (a re-run phase) replaces an earlier one.
	last := map[string]int{}
	for i, rec := range recs {
		last[rec.QuestionID] = i
	}
	for i := range recs {
		rec := recs[i]
		q, ok := byID[rec.QuestionID]
		if !ok || last[rec.QuestionID] != i {
			continue
		}
		q.Ingest = &recs[i]
		impls[rec.Impl.Label] = true
		s.Questions++
		if rec.Cached {
			s.Cached++
		}
		s.Tokens.Input += rec.Usage.InputTokens
		s.Tokens.Output += rec.Usage.OutputTokens
		if rec.Failure != "" || rec.Metrics == nil {
			s.Failures++
			continue
		}
		n++
		s.MeanClaims += float64(rec.Metrics.Claims)
		s.MeanParticular += float64(rec.Metrics.Particulars)
		s.MeanTopics += float64(rec.Metrics.Topics)
		s.MeanSyntheses += float64(rec.Metrics.Syntheses)
		if rec.Metrics.OnSessionDate != nil {
			onDate = append(onDate, *rec.Metrics.OnSessionDate)
		}
	}
	if n > 0 {
		s.MeanClaims /= float64(n)
		s.MeanParticular /= float64(n)
		s.MeanTopics /= float64(n)
		s.MeanSyntheses /= float64(n)
	}
	if len(onDate) > 0 {
		var sum float64
		for _, f := range onDate {
			sum += f
		}
		m := sum / float64(len(onDate))
		s.MeanOnSessionDate = &m
	}
	for l := range impls {
		s.Impls = append(s.Impls, l)
	}
	sort.Strings(s.Impls)
	return s
}

type key struct {
	q      string
	repeat int
}

func buildVariant(d *run.Dir, v run.Variant, byID map[string]*Question, r *Report) (VariantResult, error) {
	vr := VariantResult{Variant: v, Accuracy: map[string]Accuracy{}, ToolCalls: map[string]int{}, ExtensionCalls: map[string]int{}}
	answers, err := run.Read[run.Answer](d, "answers/"+v.Label)
	if err != nil {
		return vr, err
	}
	judgments, err := run.Read[run.Judgment](d, "judgments/"+v.Label)
	if err != nil {
		return vr, err
	}
	verdicts := map[key]run.Judgment{}
	for _, j := range judgments {
		verdicts[key{j.QuestionID, j.Repeat}] = j
		if r.Judge == nil && j.Failure == "" {
			s := j.Settings
			r.Judge = &s
		}
		vr.JudgeTokens.Input += j.Verdict.Usage.InputTokens
		vr.JudgeTokens.Output += j.Verdict.Usage.OutputTokens
	}
	offered := map[string]bool{}
	for _, e := range v.OfferedExtensions {
		offered[e] = true
		vr.ExtensionCalls[e] = 0
	}

	type tally struct{ sum, n float64 }
	perQuestion := map[string]*tally{}
	var graded int
	var toolCalls int
	for _, a := range answers {
		q, ok := byID[a.QuestionID]
		if !ok {
			continue
		}
		vr.Answers++
		o := Outcome{
			Repeat: a.Repeat, Answer: a.Output.Text, ToolCalls: a.Output.ToolCalls, Retrieved: a.Output.Retrieved,
			Tokens: map[string]PhaseTokens{"answer": tokens(a.Output.Usage)}, Failure: a.Failure,
		}
		if q.Ingest != nil && v.Kind == "implementation" {
			o.Tokens["ingest"] = tokens(q.Ingest.Usage)
		}
		vr.AnswerTokensTotal.Input += a.Output.Usage.InputTokens
		vr.AnswerTokensTotal.Output += a.Output.Usage.OutputTokens
		for name, c := range a.Output.ToolCalls {
			vr.ToolCalls[name] += c
			toolCalls += c
			if offered[name] {
				vr.ExtensionCalls[name] += c
			}
		}
		j, judged := verdicts[key{a.QuestionID, a.Repeat}]
		switch {
		case a.Failure != "":
			vr.Failures++
		case judged && j.Failure != "":
			vr.Failures++
			o.Failure = "judge: " + j.Failure
		case !judged:
			vr.Ungraded++
		default:
			c := j.Verdict.Correct
			o.Correct = &c
			o.JudgeRaw = j.Verdict.Raw
			o.Tokens["judge"] = tokens(j.Verdict.Usage)
			t := perQuestion[a.QuestionID]
			if t == nil {
				t = &tally{}
				perQuestion[a.QuestionID] = t
			}
			t.n++
			if c {
				t.sum++
			}
			graded++
		}
		q.Outcomes[v.Label] = append(q.Outcomes[v.Label], o)
	}
	if vr.Answers > 0 {
		vr.InputPerAnswer = float64(vr.AnswerTokensTotal.Input) / float64(vr.Answers)
		vr.OutputPerAnswer = float64(vr.AnswerTokensTotal.Output) / float64(vr.Answers)
		vr.ToolCallsPerAns = float64(toolCalls) / float64(vr.Answers)
	}

	sums := map[string]*tally{}
	for id, t := range perQuestion {
		for _, row := range []string{byID[id].Stratum, OverallRow} {
			s := sums[row]
			if s == nil {
				s = &tally{}
				sums[row] = s
			}
			s.sum += t.sum / t.n
			s.n++
		}
	}
	for _, row := range append(append([]string(nil), r.Strata...), OverallRow) {
		s := sums[row]
		if s == nil {
			vr.Accuracy[row] = Wilson(0, 0)
			continue
		}
		vr.Accuracy[row] = Wilson(s.sum, int(s.n))
	}
	return vr, nil
}
