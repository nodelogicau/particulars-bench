// Package cli is the particulars-bench command: phases ingest, answer, judge,
// and report, each separately runnable against one run directory.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/answer"
	"github.com/nodelogicau/particulars-bench/internal/dataset"
	"github.com/nodelogicau/particulars-bench/internal/impl"
	"github.com/nodelogicau/particulars-bench/internal/run"
	"github.com/nodelogicau/particulars-bench/internal/spec"
)

// API key variables by provider, used unless a role names another.
var apiKeyVars = map[string]string{
	agent.ProviderAnthropic: "ANTHROPIC_API_KEY",
	agent.ProviderOpenAI:    "OPENAI_API_KEY",
}

// Efforts each provider accepts: Anthropic output_config.effort, OpenAI
// reasoning_effort. An empty effort is not sent.
var efforts = map[string][]string{
	agent.ProviderAnthropic: {"low", "medium", "high", "xhigh", "max"},
	agent.ProviderOpenAI:    {"none", "minimal", "low", "medium", "high", "xhigh", "max"},
}

// Deps are the command's connections to the outside world.
type Deps struct {
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	// NewProvider returns a role's model provider; it is called only after
	// the plan is printed and only when a phase will call a model.
	NewProvider func(s agent.Settings, apiKey string) (agent.Provider, error)
	Now         func() time.Time
}

// DefaultDeps uses the process environment and the providers' APIs.
func DefaultDeps() Deps {
	return Deps{
		Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv,
		NewProvider: agent.New,
		Now:         time.Now,
	}
}

const usage = `particulars-bench measures DKF implementations as memory for an agent, on LongMemEval.

Usage:
  particulars-bench <phase> [flags]

Phases:
  ingest   replay each question's sessions into a cached workspace through one --impl
  answer   answer each question with every --impl (read-only, on a copy of the
           cached workspace) and every --baselines entry
  judge    grade a run's answers with LongMemEval's per-type prompts
  report   write report.json (bench-report/1) and report.md for a run

ingest, answer, and judge call a model and refuse to start when the API key
variable of the provider they call is unset (ANTHROPIC_API_KEY for anthropic,
OPENAI_API_KEY for openai, or the variable --api-key-env / --judge-api-key-env
names), except with --dry-run, which prints the plan and exits.

Run 'particulars-bench <phase> -h' for flags.
`

// Run executes the command and returns its exit code.
func Run(ctx context.Context, args []string, d Deps) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(d.Stdout, usage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	// Phases report progress from concurrent workers.
	d.Stderr = &lockedWriter{w: d.Stderr}
	phase := args[0]
	var fn func(context.Context, *options, Deps) error
	switch phase {
	case "ingest":
		fn = runIngest
	case "answer":
		fn = runAnswer
	case "judge":
		fn = runJudge
	case "report":
		fn = runReport
	default:
		fmt.Fprintf(d.Stderr, "unknown phase %q\n\n%s", phase, usage)
		return 2
	}
	o, err := parseFlags(phase, args[1:], d.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(d.Stderr, "particulars-bench %s: %v\n", phase, err)
		return 2
	}
	if !o.dryRun {
		for _, r := range o.roles() {
			if d.Getenv(r.keyEnv) == "" {
				fmt.Fprintf(d.Stderr, "particulars-bench %s: %s is not set; refusing to start a phase that calls a model (use --dry-run to see the plan)\n", phase, r.keyEnv)
				return 1
			}
		}
	}
	if err := fn(ctx, o, d); err != nil {
		fmt.Fprintf(d.Stderr, "particulars-bench %s: %v\n", phase, err)
		return 1
	}
	return 0
}

type options struct {
	phase         string
	impls         []impl.Impl
	ingestImpl    *impl.Impl
	variant       string
	sel           dataset.Selection
	concurrency   int
	dryRun        bool
	repeats       int
	specToolsOnly bool
	runDir        string
	resultsDir    string
	agent         agent.Settings
	judge         agent.Settings
	agentKeyEnv   string
	judgeKeyEnv   string
	judgeLabel    string
	baselines     []string
	bm25K         int
	// set names the flags given explicitly.
	set map[string]bool
}

type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(s string) error {
	*l = append(*l, s)
	return nil
}

var labelRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func parseFlags(phase string, args []string, stderr io.Writer) (*options, error) {
	fs := flag.NewFlagSet("particulars-bench "+phase, flag.ContinueOnError)
	fs.SetOutput(stderr)
	o := &options{phase: phase}
	var impls listFlag
	var ingestImpl, types, ids, baselines string
	var agentProvider, agentExtra, judgeProvider, judgeExtra string
	fs.Var(&impls, "impl", "implementation under test, `label='command … {workspace} …'` (repeatable)")
	fs.StringVar(&ingestImpl, "ingest-impl", "", "implementation that ingests for the answer phase, `label='command'` (default: the only --impl)")
	fs.StringVar(&o.variant, "variant", "oracle", "dataset variant: "+strings.Join(dataset.VariantNames(), ", "))
	fs.IntVar(&o.sel.PerType, "per-type", 10, "questions sampled per question type (abstention is its own type)")
	fs.Uint64Var(&o.sel.Seed, "seed", 1, "sampling seed")
	fs.StringVar(&types, "types", "", "comma-separated question types to keep (including \"abstention\")")
	fs.StringVar(&ids, "ids", "", "comma-separated question ids, replacing the sample")
	fs.IntVar(&o.sel.Limit, "limit", 0, "cap on the number of questions")
	fs.IntVar(&o.concurrency, "concurrency", 4, "questions processed at once")
	fs.BoolVar(&o.dryRun, "dry-run", false, "print the plan and exit without calling a model")
	fs.IntVar(&o.repeats, "repeats", 1, "answers per question per variant, for variance")
	fs.BoolVar(&o.specToolsOnly, "spec-tools-only", false, "answer phase: withhold every tool outside the DKF specification")
	fs.StringVar(&o.runDir, "run", "", "run directory (default: a new one under --results for ingest and answer; the latest for judge and report)")
	fs.StringVar(&o.resultsDir, "results", "results", "directory holding run directories")
	fs.StringVar(&o.agent.Model, "model", "claude-opus-5-5", "model for ingest and answers")
	fs.StringVar(&o.agent.Effort, "effort", "medium", "effort for ingest and answers")
	fs.Int64Var(&o.agent.MaxTokens, "max-tokens", 16000, "max_tokens per model call")
	fs.IntVar(&o.agent.MaxTurns, "max-turns", 30, "model calls per agent loop (one session, or one answer)")
	fs.StringVar(&agentProvider, "provider", agent.ProviderAnthropic, "provider for ingest and answers: "+strings.Join(agent.Providers, ", "))
	fs.StringVar(&o.agent.BaseURL, "base-url", "", "API base URL for ingest and answers (openai default: "+agent.DefaultOpenAIBaseURL+")")
	fs.StringVar(&o.agentKeyEnv, "api-key-env", "", "`variable` holding the API key for ingest and answers (default: ANTHROPIC_API_KEY or OPENAI_API_KEY by provider)")
	fs.StringVar(&agentExtra, "extra-body", "", "JSON object merged into every ingest and answer request body")
	fs.StringVar(&o.judge.Model, "judge-model", "claude-opus-5-5", "judge model")
	fs.StringVar(&o.judge.Effort, "judge-effort", "low", "judge effort (openai: sent only when given; otherwise temperature 0)")
	fs.StringVar(&judgeProvider, "judge-provider", agent.ProviderAnthropic, "judge provider: "+strings.Join(agent.Providers, ", "))
	fs.StringVar(&o.judge.BaseURL, "judge-base-url", "", "judge API base URL (openai default: "+agent.DefaultOpenAIBaseURL+")")
	fs.StringVar(&o.judgeKeyEnv, "judge-api-key-env", "", "`variable` holding the judge's API key (default: by provider)")
	fs.StringVar(&judgeExtra, "judge-extra-body", "", "JSON object merged into every judge request body")
	fs.StringVar(&o.judgeLabel, "judge-label", "", "name of a judge's verdicts in the run; judge writes and report reads them (default: the judge model id)")
	fs.StringVar(&baselines, "baselines", "", "answer phase: comma-separated baselines: "+strings.Join(answer.BaselineNames, ", "))
	fs.IntVar(&o.bm25K, "bm25-k", 3, "sessions the bm25 baseline retrieves")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	o.set = map[string]bool{}
	fs.Visit(func(f *flag.Flag) { o.set[f.Name] = true })
	o.judge.MaxTokens, o.judge.MaxTurns = 4000, 1
	if err := configureRole("", agentProvider, agentExtra, &o.agent, &o.agentKeyEnv, o.set); err != nil {
		return nil, err
	}
	if err := configureRole("judge-", judgeProvider, judgeExtra, &o.judge, &o.judgeKeyEnv, o.set); err != nil {
		return nil, err
	}
	if o.judge.Provider == agent.ProviderOpenAI && o.judge.Effort == "" {
		// LongMemEval grades at temperature 0; reasoning models reject it.
		zero := 0.0
		o.judge.Temperature = &zero
	}
	if o.judgeLabel == "" {
		o.judgeLabel = strings.Trim(labelChars.ReplaceAllString(o.judge.Model, "-"), "-._")
	}
	if !labelRE.MatchString(o.judgeLabel) {
		return nil, fmt.Errorf("--judge-label %q: use letters, digits, '.', '_', '-'", o.judgeLabel)
	}
	seen := map[string]bool{}
	for _, s := range impls {
		im, err := impl.Parse(s)
		if err != nil {
			return nil, err
		}
		if !labelRE.MatchString(im.Label) {
			return nil, fmt.Errorf("--impl label %q: use letters, digits, '.', '_', '-'", im.Label)
		}
		if seen[im.Label] {
			return nil, fmt.Errorf("--impl label %q given twice", im.Label)
		}
		seen[im.Label] = true
		o.impls = append(o.impls, im)
	}
	if ingestImpl != "" {
		im, err := impl.Parse(ingestImpl)
		if err != nil {
			return nil, err
		}
		o.ingestImpl = &im
	}
	o.sel.Strata = split(types)
	o.sel.IDs = split(ids)
	for _, b := range split(baselines) {
		known := false
		for _, n := range answer.BaselineNames {
			known = known || n == b
		}
		if !known {
			return nil, fmt.Errorf("unknown baseline %q (known: %v)", b, answer.BaselineNames)
		}
		if seen[b] {
			return nil, fmt.Errorf("label %q is both an --impl and a baseline", b)
		}
		seen[b] = true
		o.baselines = append(o.baselines, b)
	}
	if o.concurrency < 1 || o.repeats < 1 || o.agent.MaxTurns < 1 || o.bm25K < 1 {
		return nil, fmt.Errorf("--concurrency, --repeats, --max-turns, and --bm25-k must be at least 1")
	}
	return o, nil
}

// role is a model role a phase calls.
type role struct {
	name     string
	settings agent.Settings
	keyEnv   string
}

// roles returns the model roles the phase calls.
func (o *options) roles() []role {
	switch o.phase {
	case "ingest", "answer":
		return []role{{"agent", o.agent, o.agentKeyEnv}}
	case "judge":
		return []role{{"judge", o.judge, o.judgeKeyEnv}}
	}
	return nil
}

// provider returns the provider for a role, keyed from the environment.
func provider(d Deps, r role) (agent.Provider, error) {
	return d.NewProvider(r.settings, d.Getenv(r.keyEnv))
}

// configureRole validates a role's provider flags and applies the provider's
// defaults: on openai the effort is sent only when given and the base URL
// defaults to OpenAI's; on anthropic the provider field stays empty so that
// recorded settings and cache keys are as before providers existed.
func configureRole(prefix, providerName, extraBody string, s *agent.Settings, keyEnv *string, set map[string]bool) error {
	if _, ok := efforts[providerName]; !ok {
		return fmt.Errorf("--%sprovider %q: use one of %v", prefix, providerName, agent.Providers)
	}
	if providerName == agent.ProviderOpenAI {
		s.Provider = providerName
		if !set[prefix+"effort"] {
			s.Effort = ""
		}
		if s.BaseURL == "" {
			s.BaseURL = agent.DefaultOpenAIBaseURL
		}
	}
	s.BaseURL = strings.TrimRight(s.BaseURL, "/")
	if s.Effort != "" && !contains(efforts[providerName], s.Effort) {
		return fmt.Errorf("--%seffort %q: %s accepts %v", prefix, s.Effort, providerName, efforts[providerName])
	}
	if extraBody != "" {
		if err := json.Unmarshal([]byte(extraBody), &s.ExtraBody); err != nil || s.ExtraBody == nil {
			return fmt.Errorf("--%sextra-body: not a JSON object: %s", prefix, extraBody)
		}
	}
	if *keyEnv == "" {
		*keyEnv = apiKeyVars[providerName]
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// labelChars matches the characters a label cannot hold.
var labelChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// loaded is a run's questions and directory.
type loaded struct {
	meta      run.Meta
	questions []dataset.Question
	dir       *run.Dir // nil in a dry run that would create a new run
}

// load resolves the run directory and its sample. An existing run keeps the
// dataset and questions it started with; a new one samples per the flags.
// In a dry run nothing is written.
func load(o *options, d Deps, createIfMissing bool) (*loaded, error) {
	path := o.runDir
	if path == "" {
		if createIfMissing {
			path = filepath.Join(o.resultsDir, d.Now().UTC().Format("20060102T150405Z"))
		} else {
			latest, err := run.Latest(o.resultsDir)
			if err != nil {
				return nil, err
			}
			path = latest
		}
	}
	existing := &run.Dir{Path: path}
	meta, ok, err := existing.Meta()
	if err != nil {
		return nil, err
	}
	if !ok && !createIfMissing {
		return nil, fmt.Errorf("%s is not a run (no run.json)", path)
	}
	variant := o.variant
	if ok {
		variant = meta.Dataset.Variant
		if o.set["variant"] && o.variant != variant {
			return nil, fmt.Errorf("run %s uses dataset variant %q, not %q", path, variant, o.variant)
		}
		for _, name := range []string{"per-type", "seed", "types", "ids", "limit"} {
			if o.set[name] {
				fmt.Fprintf(d.Stderr, "note: --%s ignored: run %s keeps the sample it started with\n", name, path)
			}
		}
	}
	file, f, err := dataset.Ensure(variant, d.Stderr)
	if err != nil {
		return nil, err
	}
	all, err := dataset.Load(file)
	if err != nil {
		return nil, err
	}
	l := &loaded{}
	if ok {
		fmt.Fprintf(d.Stderr, "using run %s (its dataset and %d questions)\n", path, len(meta.QuestionIDs))
		qs, err := dataset.Sample(all, dataset.Selection{IDs: meta.QuestionIDs})
		if err != nil {
			return nil, err
		}
		l.meta, l.questions = meta, qs
	} else {
		qs, err := dataset.Sample(all, o.sel)
		if err != nil {
			return nil, err
		}
		l.questions = qs
		l.meta = run.Meta{
			Created: d.Now().UTC(), HarnessCommit: harnessCommit(), DKFFormat: spec.DKFFormat,
			Dataset:   run.Dataset{Variant: variant, File: f.Name, Repository: dataset.Repository, Revision: dataset.Revision, SHA256: f.SHA256},
			Selection: o.sel,
		}
		for _, q := range qs {
			l.meta.QuestionIDs = append(l.meta.QuestionIDs, q.ID)
		}
	}
	if o.dryRun && !ok {
		return l, nil
	}
	if l.dir, err = run.Open(path); err != nil {
		return nil, err
	}
	if !ok {
		if err := l.dir.WriteMeta(l.meta); err != nil {
			return nil, err
		}
	}
	return l, nil
}

func harnessCommit() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		var rev, modified string
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
		if rev != "" {
			if modified == "true" {
				rev += "-dirty"
			}
			return rev
		}
	}
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	rev := strings.TrimSpace(string(out))
	if err := exec.Command("git", "diff", "--quiet", "HEAD").Run(); err != nil {
		rev += "-dirty"
	}
	return rev
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// forEach runs f for i in [0, n) with at most conc at once.
func forEach(ctx context.Context, n, conc int, f func(i int)) {
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			f(i)
		}(i)
	}
	wg.Wait()
}
