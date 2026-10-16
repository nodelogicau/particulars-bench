// Package run is the directory a run's phases write to and read from, so that
// each phase can run on its own.
package run

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/answer"
	"github.com/nodelogicau/particulars-bench/internal/dataset"
	"github.com/nodelogicau/particulars-bench/internal/impl"
	"github.com/nodelogicau/particulars-bench/internal/judge"
	"github.com/nodelogicau/particulars-bench/internal/workspace"
)

// Dataset identifies the dataset file a run used.
type Dataset struct {
	Variant    string `json:"variant"`
	File       string `json:"file"`
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	SHA256     string `json:"sha256"`
}

// Meta is fixed when a run starts: its dataset and sample.
type Meta struct {
	Created       time.Time         `json:"created"`
	HarnessCommit string            `json:"harness_commit"`
	DKFFormat     string            `json:"dkf_format"`
	Dataset       Dataset           `json:"dataset"`
	Selection     dataset.Selection `json:"selection"`
	QuestionIDs   []string          `json:"question_ids"`
}

// IngestRecord is one question's ingest.
type IngestRecord struct {
	QuestionID   string             `json:"question_id"`
	Impl         impl.Impl          `json:"impl"`
	ServerInfo   impl.ServerInfo    `json:"server_info"`
	Key          string             `json:"key,omitempty"`
	Cached       bool               `json:"cached"`
	Settings     agent.Settings     `json:"settings"`
	PromptSHA256 string             `json:"prompt_sha256"`
	Sessions     int                `json:"sessions"`
	Usage        agent.Usage        `json:"usage"`
	Metrics      *workspace.Metrics `json:"metrics,omitempty"`
	Failure      string             `json:"failure,omitempty"`
}

// Variant describes one answering configuration.
type Variant struct {
	Label string `json:"label"`
	// Kind is "implementation" or "baseline".
	Kind          string            `json:"kind"`
	Command       string            `json:"command,omitempty"`
	ServerInfo    impl.ServerInfo   `json:"server_info,omitzero"`
	Settings      agent.Settings    `json:"settings"`
	Prompts       map[string]string `json:"prompt_sha256"`
	SpecToolsOnly bool              `json:"spec_tools_only"`
	// OfferedExtensions are the tools outside the specification offered to
	// the model in the answer phase.
	OfferedExtensions []string   `json:"offered_extensions"`
	IngestImpl        *impl.Impl `json:"ingest_impl,omitempty"`
	BM25K             int        `json:"bm25_k,omitempty"`
	Repeats           int        `json:"repeats"`
}

// Answer is one answer to one question by one variant.
type Answer struct {
	Variant    string        `json:"variant"`
	QuestionID string        `json:"question_id"`
	Repeat     int           `json:"repeat"`
	IngestKey  string        `json:"ingest_key,omitempty"`
	Output     answer.Output `json:"output"`
	// Failure is an infrastructure failure: the answer is excluded from
	// accuracy and counted separately.
	Failure string `json:"failure,omitempty"`
}

// Judgment grades one answer.
type Judgment struct {
	// Judge labels the judge, so that verdicts from several judges of
	// the same answers sit side by side.
	Judge      string         `json:"judge,omitempty"`
	Variant    string         `json:"variant"`
	QuestionID string         `json:"question_id"`
	Repeat     int            `json:"repeat"`
	Settings   agent.Settings `json:"settings"`
	Verdict    judge.Verdict  `json:"verdict"`
	Failure    string         `json:"failure,omitempty"`
}

// JudgmentStream is the stream of one judge's verdicts on one variant.
func JudgmentStream(judge, variant string) string {
	return "judgments/" + judge + "/" + variant
}

// Dir is a run directory.
type Dir struct {
	Path string
	mu   sync.Mutex
}

// Open returns the run directory at path, creating it.
func Open(path string) (*Dir, error) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, err
	}
	return &Dir{Path: path}, nil
}

// Latest returns the most recently named run under root.
func Latest(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("no runs under %s: %w", root, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(root, e.Name(), "run.json")); err == nil {
				names = append(names, e.Name())
			}
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no runs under %s", root)
	}
	sort.Strings(names)
	return filepath.Join(root, names[len(names)-1]), nil
}

func (d *Dir) file(name string) string { return filepath.Join(d.Path, name) }

// Meta reads run.json; ok is false when the run has not started.
func (d *Dir) Meta() (m Meta, ok bool, err error) {
	ok, err = readJSON(d.file("run.json"), &m)
	return m, ok, err
}

// WriteMeta writes run.json.
func (d *Dir) WriteMeta(m Meta) error { return workspace.WriteJSON(d.file("run.json"), m) }

// WriteVariant records a variant's description.
func (d *Dir) WriteVariant(v Variant) error {
	if err := os.MkdirAll(d.file("variants"), 0o755); err != nil {
		return err
	}
	return workspace.WriteJSON(d.file(filepath.Join("variants", v.Label+".json")), v)
}

// Variants reads every recorded variant, sorted by label.
func (d *Dir) Variants() ([]Variant, error) {
	paths, _ := filepath.Glob(d.file(filepath.Join("variants", "*.json")))
	sort.Strings(paths)
	var out []Variant
	for _, p := range paths {
		var v Variant
		if _, err := readJSON(p, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, nil
}

// Judges lists the labels of the judges with verdicts in the run, sorted.
func (d *Dir) Judges() ([]string, error) {
	entries, err := os.ReadDir(d.file("judgments"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// Reset empties a JSONL stream ("ingest", "answers/<label>",
// "judgments/<judge>/<label>") before a phase rewrites it.
func (d *Dir) Reset(stream string) error {
	p := d.file(stream + ".jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, nil, 0o644)
}

// Append adds a record to a JSONL stream.
func (d *Dir) Append(stream string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.file(stream + ".jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Read decodes every record of a stream into a slice of T.
func Read[T any](d *Dir, stream string) ([]T, error) {
	f, err := os.Open(d.file(stream + ".jsonl"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return nil, fmt.Errorf("%s: %w", stream, err)
		}
		out = append(out, v)
	}
	return out, sc.Err()
}

func readJSON(path string, v any) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, v)
}
