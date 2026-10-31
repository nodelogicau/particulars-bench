package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/answer"
	"github.com/nodelogicau/particulars-bench/internal/dataset"
	"github.com/nodelogicau/particulars-bench/internal/impl"
	"github.com/nodelogicau/particulars-bench/internal/ingest"
	"github.com/nodelogicau/particulars-bench/internal/judge"
	"github.com/nodelogicau/particulars-bench/internal/report"
	"github.com/nodelogicau/particulars-bench/internal/run"
	"github.com/nodelogicau/particulars-bench/internal/workspace"
	"github.com/nodelogicau/particulars-bench/prompts"
)

// printPlan prints what a phase will do before its first model call.
func printPlan(w io.Writer, o *options, l *loaded, ingestImpl *impl.Impl, answerVariants []string, judgeAnswers int) {
	m := l.meta
	fmt.Fprintf(w, "Plan (%s)\n", o.phase)
	fmt.Fprintf(w, "  dataset: %s (%s, sha256 %s)\n", m.Dataset.Variant, m.Dataset.File, m.Dataset.SHA256)
	fmt.Fprintf(w, "  seed %d, %d per type; %d questions\n", m.Selection.Seed, m.Selection.PerType, len(l.questions))
	for _, r := range o.roles() {
		fmt.Fprintf(w, "  %s model: %s\n", r.name, describeRole(r))
	}
	byStratum := map[string][2]int{}
	sessions := 0
	for _, q := range l.questions {
		c := byStratum[q.Stratum()]
		c[0]++
		c[1] += len(q.Sessions)
		byStratum[q.Stratum()] = c
		sessions += len(q.Sessions)
	}
	var names []string
	for n := range byStratum {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "    %-28s %3d questions, %4d sessions\n", n, byStratum[n][0], byStratum[n][1])
	}
	if ingestImpl != nil {
		fmt.Fprintf(w, "  ingest with %s: %d sessions to replay (cached workspaces are reused and replay nothing)\n", ingestImpl.Label, sessions)
		fmt.Fprintf(w, "    model calls: %d to %d (%s, up to %d per session)\n", sessions, sessions*o.agent.MaxTurns, o.agent.Model, o.agent.MaxTurns)
	}
	for _, v := range answerVariants {
		n := len(l.questions) * o.repeats
		if isBaseline(v) {
			fmt.Fprintf(w, "  answer %s: %d answers, %d model calls (%s)\n", v, n, n, o.agent.Model)
		} else {
			fmt.Fprintf(w, "  answer %s: %d answers, %d to %d model calls (%s)\n", v, n, n, n*o.agent.MaxTurns, o.agent.Model)
		}
	}
	if judgeAnswers > 0 {
		fmt.Fprintf(w, "  judge %s: %d model calls (%s)\n", o.judgeLabel, judgeAnswers, o.judge.Model)
	}
}

// describeRole names a role's provider, model, endpoint, parameters, and key
// variable.
func describeRole(r role) string {
	s := r.settings
	base := s.BaseURL
	if base == "" {
		base = "SDK default"
	}
	var params []string
	if s.Effort != "" {
		params = append(params, "effort "+s.Effort)
	}
	if s.Temperature != nil {
		params = append(params, fmt.Sprintf("temperature %g", *s.Temperature))
	}
	if len(s.ExtraBody) > 0 {
		b, _ := json.Marshal(s.ExtraBody)
		params = append(params, "extra body "+string(b))
	}
	out := fmt.Sprintf("%s %s at %s, key %s", s.ProviderName(), s.Model, base, r.keyEnv)
	if len(params) > 0 {
		out += " (" + strings.Join(params, ", ") + ")"
	}
	return out
}

func isBaseline(label string) bool {
	for _, b := range answer.BaselineNames {
		if b == label {
			return true
		}
	}
	return false
}

func cacheRoot() (ingest.Cache, error) {
	root, err := dataset.CacheDir()
	if err != nil {
		return ingest.Cache{}, err
	}
	return ingest.Cache{Root: filepath.Join(root, "longmemeval", "workspaces")}, nil
}

// ingestAll ensures every question is ingested, recording each in the run's
// ingest stream, and returns the manifests by question id (absent on
// failure) and failures by question id.
func ingestAll(ctx context.Context, o *options, d Deps, l *loaded, im impl.Impl, m agent.Provider, tmp string) (map[string]*ingest.Manifest, map[string]string, error) {
	cache, err := cacheRoot()
	if err != nil {
		return nil, nil, err
	}
	probe, err := ingest.ProbeImpl(ctx, im, tmp)
	if err != nil {
		return nil, nil, fmt.Errorf("probing ingest implementation %s: %w", im.Label, err)
	}
	fmt.Fprintf(d.Stderr, "ingest implementation %s: %s %s\n", im.Label, probe.Info.Name, probe.Info.Version)
	ing := &ingest.Ingester{
		Cache: cache, Impl: im, Model: m, Settings: o.agent,
		Variant: l.meta.Dataset.Variant, DatasetSHA256: l.meta.Dataset.SHA256, Probe: probe,
	}
	if err := l.dir.Reset("ingest"); err != nil {
		return nil, nil, err
	}
	var mu sync.Mutex
	manifests := map[string]*ingest.Manifest{}
	failures := map[string]string{}
	var appendErr error
	forEach(ctx, len(l.questions), o.concurrency, func(i int) {
		q := l.questions[i]
		man, cached, err := ing.Ensure(ctx, q)
		rec := run.IngestRecord{
			QuestionID: q.ID, Impl: im, ServerInfo: probe.Info, Key: ing.Key(q).Key(), Cached: cached,
			Settings: o.agent, PromptSHA256: prompts.Hash(ingest.PromptName), Sessions: len(q.Sessions),
		}
		if man != nil {
			rec.Usage = man.Usage
			rec.ToolCalls, rec.ToolErrorsByTool, rec.ToolErrors = man.ToolCalls()
		}
		status := "ingested"
		if cached {
			status = "cached"
		}
		if err != nil {
			rec.Failure = err.Error()
			status = "FAILED: " + firstLine(err.Error())
		} else {
			metrics := man.Metrics
			rec.Metrics = &metrics
		}
		mu.Lock()
		if err != nil {
			failures[q.ID] = rec.Failure
		} else {
			manifests[q.ID] = man
		}
		mu.Unlock()
		if err := l.dir.Append("ingest", rec); err != nil {
			mu.Lock()
			appendErr = err
			mu.Unlock()
		}
		fmt.Fprintf(d.Stderr, "ingest %s %s: %s\n", im.Label, q.ID, status)
	})
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	return manifests, failures, appendErr
}

func runIngest(ctx context.Context, o *options, d Deps) error {
	im, err := ingestImplFor(o)
	if err != nil {
		return err
	}
	if im == nil {
		return fmt.Errorf("ingest needs one --impl")
	}
	l, err := load(o, d, true)
	if err != nil {
		return err
	}
	printPlan(d.Stdout, o, l, im, nil, 0)
	if o.dryRun {
		return nil
	}
	tmp, err := os.MkdirTemp("", "particulars-bench-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	m, err := provider(d, o.roles()[0])
	if err != nil {
		return err
	}
	_, failures, err := ingestAll(ctx, o, d, l, *im, m, tmp)
	if err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "ingested %d questions (%d failed) into run %s\n", len(l.questions), len(failures), l.dir.Path)
	return nil
}

// ingestImplFor returns the implementation that ingests: --ingest-impl, else
// the only --impl. With several --impl and no --ingest-impl it is an error.
func ingestImplFor(o *options) (*impl.Impl, error) {
	if o.ingestImpl != nil {
		return o.ingestImpl, nil
	}
	switch len(o.impls) {
	case 0:
		return nil, nil
	case 1:
		return &o.impls[0], nil
	default:
		return nil, fmt.Errorf("several --impl given: name the one that ingests with --ingest-impl")
	}
}

func runAnswer(ctx context.Context, o *options, d Deps) error {
	if len(o.impls) == 0 && len(o.baselines) == 0 {
		return fmt.Errorf("answer needs at least one --impl or --baselines")
	}
	var ingestImpl *impl.Impl
	if len(o.impls) > 0 {
		var err error
		if ingestImpl, err = ingestImplFor(o); err != nil {
			return err
		}
	}
	l, err := load(o, d, true)
	if err != nil {
		return err
	}
	var labels []string
	for _, im := range o.impls {
		labels = append(labels, im.Label)
	}
	labels = append(labels, o.baselines...)
	printPlan(d.Stdout, o, l, ingestImpl, labels, 0)
	if o.dryRun {
		return nil
	}
	tmp, err := os.MkdirTemp("", "particulars-bench-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	m, err := provider(d, o.roles()[0])
	if err != nil {
		return err
	}

	var manifests map[string]*ingest.Manifest
	var ingestFailures map[string]string
	if ingestImpl != nil {
		if manifests, ingestFailures, err = ingestAll(ctx, o, d, l, *ingestImpl, m, tmp); err != nil {
			return err
		}
	}
	cache, err := cacheRoot()
	if err != nil {
		return err
	}

	for _, im := range o.impls {
		v := run.Variant{
			Label: im.Label, Kind: "implementation", Command: im.Command, Settings: o.agent,
			Prompts:       map[string]string{answer.MemoryPrompt: prompts.Hash(answer.MemoryPrompt), ingest.PromptName: prompts.Hash(ingest.PromptName)},
			SpecToolsOnly: o.specToolsOnly, IngestImpl: ingestImpl, Repeats: o.repeats, OfferedExtensions: []string{},
		}
		var once sync.Once
		err := answerEach(ctx, o, d, l, im.Label, func(q dataset.Question, repeat int) run.Answer {
			a := run.Answer{Variant: im.Label, QuestionID: q.ID, Repeat: repeat}
			man := manifests[q.ID]
			if man == nil {
				a.Failure = "ingest: " + ingestFailures[q.ID]
				return a
			}
			a.IngestKey = man.Key
			out, info, ext, err := answer.WithMemory(ctx, m, o.agent, im, o.specToolsOnly, cache, man.Key, tmp, q)
			a.Output = out
			if err != nil {
				a.Failure = err.Error()
				return a
			}
			once.Do(func() {
				v.ServerInfo = info
				if ext != nil {
					v.OfferedExtensions = ext
				}
			})
			return a
		})
		if err != nil {
			return err
		}
		if err := l.dir.WriteVariant(v); err != nil {
			return err
		}
	}
	for _, b := range o.baselines {
		v := run.Variant{
			Label: b, Kind: "baseline", Settings: o.agent, Repeats: o.repeats, OfferedExtensions: []string{},
			Prompts: map[string]string{answer.BaselinePrompt: prompts.Hash(answer.BaselinePrompt)},
		}
		if b == answer.BM25 {
			v.BM25K = o.bm25K
		}
		err := answerEach(ctx, o, d, l, b, func(q dataset.Question, repeat int) run.Answer {
			a := run.Answer{Variant: b, QuestionID: q.ID, Repeat: repeat}
			out, err := answer.Baseline(ctx, m, o.agent, b, o.bm25K, q)
			a.Output = out
			if err != nil {
				a.Failure = err.Error()
			}
			return a
		})
		if err != nil {
			return err
		}
		if err := l.dir.WriteVariant(v); err != nil {
			return err
		}
	}
	fmt.Fprintf(d.Stdout, "answers written to run %s; next: particulars-bench judge --run %s\n", l.dir.Path, l.dir.Path)
	return nil
}

// answerEach answers every question repeats times with f, rewriting the
// variant's answer stream.
func answerEach(ctx context.Context, o *options, d Deps, l *loaded, label string, f func(dataset.Question, int) run.Answer) error {
	stream := "answers/" + label
	if err := l.dir.Reset(stream); err != nil {
		return err
	}
	type job struct {
		q      dataset.Question
		repeat int
	}
	var jobs []job
	for _, q := range l.questions {
		for r := 0; r < o.repeats; r++ {
			jobs = append(jobs, job{q, r})
		}
	}
	var mu sync.Mutex
	var appendErr error
	forEach(ctx, len(jobs), o.concurrency, func(i int) {
		j := jobs[i]
		a := f(j.q, j.repeat)
		status := "answered"
		if a.Failure != "" {
			status = "FAILED: " + firstLine(a.Failure)
		}
		if err := l.dir.Append(stream, a); err != nil {
			mu.Lock()
			appendErr = err
			mu.Unlock()
		}
		fmt.Fprintf(d.Stderr, "answer %s %s#%d: %s\n", label, j.q.ID, j.repeat, status)
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return appendErr
}

func runJudge(ctx context.Context, o *options, d Deps) error {
	l, err := load(o, d, false)
	if err != nil {
		return err
	}
	variants, err := l.dir.Variants()
	if err != nil {
		return err
	}
	byID := map[string]dataset.Question{}
	for _, q := range l.questions {
		byID[q.ID] = q
	}
	type pending struct {
		variant string
		answers []run.Answer
	}
	var work []pending
	total := 0
	for _, v := range variants {
		answers, err := run.Read[run.Answer](l.dir, "answers/"+v.Label)
		if err != nil {
			return err
		}
		var gradable []run.Answer
		for _, a := range answers {
			if a.Failure == "" {
				gradable = append(gradable, a)
			}
		}
		work = append(work, pending{v.Label, gradable})
		total += len(gradable)
	}
	if len(work) == 0 {
		return fmt.Errorf("run %s has no answers to judge", l.dir.Path)
	}
	printPlan(d.Stdout, o, l, nil, nil, total)
	if o.dryRun {
		return nil
	}
	m, err := provider(d, o.roles()[0])
	if err != nil {
		return err
	}
	for _, p := range work {
		stream := run.JudgmentStream(o.judgeLabel, p.variant)
		if err := l.dir.Reset(stream); err != nil {
			return err
		}
		var mu sync.Mutex
		var appendErr error
		forEach(ctx, len(p.answers), o.concurrency, func(i int) {
			a := p.answers[i]
			j := run.Judgment{Judge: o.judgeLabel, Variant: a.Variant, QuestionID: a.QuestionID, Repeat: a.Repeat, Settings: o.judge}
			q, ok := byID[a.QuestionID]
			if !ok {
				return
			}
			verdict, err := judge.Grade(ctx, m, o.judge, q, a.Output.Text)
			j.Verdict = verdict
			status := fmt.Sprintf("correct=%t", verdict.Correct)
			if err != nil {
				j.Failure = err.Error()
				status = "FAILED: " + firstLine(err.Error())
			}
			if err := l.dir.Append(stream, j); err != nil {
				mu.Lock()
				appendErr = err
				mu.Unlock()
			}
			fmt.Fprintf(d.Stderr, "judge %s %s#%d: %s\n", a.Variant, a.QuestionID, a.Repeat, status)
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if appendErr != nil {
			return appendErr
		}
	}
	fmt.Fprintf(d.Stdout, "judgments written to run %s; next: particulars-bench report --run %s\n", l.dir.Path, l.dir.Path)
	return nil
}

func runReport(ctx context.Context, o *options, d Deps) error {
	l, err := load(o, d, false)
	if err != nil {
		return err
	}
	primary, err := primaryJudge(o, l.dir)
	if err != nil {
		return err
	}
	r, err := report.Build(l.dir, l.meta, l.questions, d.Now(), primary)
	if err != nil {
		return err
	}
	jsonPath := filepath.Join(l.dir.Path, "report.json")
	mdPath := filepath.Join(l.dir.Path, "report.md")
	if err := workspace.WriteJSON(jsonPath, r); err != nil {
		return err
	}
	md := report.Markdown(r)
	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		return err
	}
	fmt.Fprint(d.Stdout, md)
	fmt.Fprintf(d.Stderr, "wrote %s and %s\n", jsonPath, mdPath)
	return nil
}

// primaryJudge picks the judge whose verdicts score the report: the one
// --judge-label or --judge-model names, else the only judge, else the
// default judge model's when present.
func primaryJudge(o *options, d *run.Dir) (string, error) {
	judges, err := d.Judges()
	if err != nil {
		return "", err
	}
	if len(judges) == 0 || contains(judges, o.judgeLabel) {
		return o.judgeLabel, nil
	}
	if o.set["judge-label"] || o.set["judge-model"] {
		return "", fmt.Errorf("run %s has no verdicts from judge %q (judges: %v)", d.Path, o.judgeLabel, judges)
	}
	if len(judges) == 1 {
		return judges[0], nil
	}
	return "", fmt.Errorf("run %s has several judges %v: choose the primary one with --judge-label", d.Path, judges)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}
