// Package ingest replays a question's sessions into a fresh workspace through
// one implementation and caches the result under a key of everything that
// determined it.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/impl"
	"github.com/nodelogicau/particulars-bench/internal/workspace"
)

// Probe is what an implementation teaches on an empty workspace, with the
// workspace's path and id replaced by placeholders so that it is the same
// for every workspace.
type Probe struct {
	Info         impl.ServerInfo `json:"server_info"`
	Instructions string          `json:"instructions"`
	Tools        json.RawMessage `json:"tools"`
}

// ProbeImpl starts im on an empty workspace under tmpRoot and records its
// serverInfo, initialize instructions, and tools/list.
func ProbeImpl(ctx context.Context, im impl.Impl, tmpRoot string) (Probe, error) {
	dir, err := os.MkdirTemp(tmpRoot, "probe-")
	if err != nil {
		return Probe{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	id, err := workspace.Create(dir)
	if err != nil {
		return Probe{}, err
	}
	srv, err := impl.Start(ctx, im, dir)
	if err != nil {
		return Probe{}, err
	}
	defer func() { _ = srv.Close() }()
	tools, err := json.Marshal(srv.Tools)
	if err != nil {
		return Probe{}, err
	}
	norm := normaliser(dir, srv.Workspace, id)
	return Probe{
		Info:         srv.Info,
		Instructions: norm.Replace(srv.Instructions),
		Tools:        json.RawMessage(norm.Replace(string(tools))),
	}, nil
}

func normaliser(paths ...string) *strings.Replacer {
	id := paths[len(paths)-1]
	var pairs []string
	seen := map[string]bool{}
	for _, p := range paths[:len(paths)-1] {
		for _, v := range []string{p, strings.TrimPrefix(p, "/private")} {
			if v != "" && !seen[v] {
				seen[v] = true
				pairs = append(pairs, v, "{workspace}")
			}
		}
	}
	return strings.NewReplacer(append(pairs, id, "{workspace-id}")...)
}

// KeyInputs is everything that determines an ingested workspace.
type KeyInputs struct {
	DatasetVariant string         `json:"dataset_variant"`
	DatasetSHA256  string         `json:"dataset_sha256"`
	QuestionID     string         `json:"question_id"`
	Settings       agent.Settings `json:"settings"`
	PromptSHA256   string         `json:"prompt_sha256"`
	Probe          Probe          `json:"probe"`
}

// Key is the SHA-256 of the canonical JSON of the inputs.
func (k KeyInputs) Key() string {
	b, err := json.Marshal(k)
	if err != nil {
		panic(err) // plain data
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// SessionRecord is what one session's replay did.
type SessionRecord struct {
	ID         string         `json:"id"`
	Date       string         `json:"date"`
	Turns      int            `json:"turns"`
	Capped     bool           `json:"capped,omitempty"`
	ToolCalls  map[string]int `json:"tool_calls,omitempty"`
	ToolErrors int            `json:"tool_errors,omitempty"`
	// ToolErrorsByTool counts ToolErrors by tool name.
	ToolErrorsByTool map[string]int          `json:"tool_errors_by_tool,omitempty"`
	ToolErrorSamples []agent.ToolErrorSample `json:"tool_error_samples,omitempty"`
	Claims           []string                `json:"claims,omitempty"`
}

// Manifest is written last, after a successful ingest. A cached workspace is
// valid only when its manifest exists and every file matches it.
type Manifest struct {
	Key      string               `json:"key"`
	Inputs   KeyInputs            `json:"inputs"`
	Created  time.Time            `json:"created"`
	Usage    agent.Usage          `json:"usage"`
	Sessions []SessionRecord      `json:"sessions"`
	Metrics  workspace.Metrics    `json:"metrics"`
	Files    []workspace.FileHash `json:"files"`
}

// ToolCalls sums the sessions' tool calls and tool errors by tool, and their
// tool errors in all. Manifests written before errors were kept by tool have
// the total only.
func (m *Manifest) ToolCalls() (calls, errorsByTool map[string]int, errors int) {
	calls, errorsByTool = map[string]int{}, map[string]int{}
	for _, s := range m.Sessions {
		for name, n := range s.ToolCalls {
			calls[name] += n
		}
		for name, n := range s.ToolErrorsByTool {
			errorsByTool[name] += n
		}
		errors += s.ToolErrors
	}
	return calls, errorsByTool, errors
}

// ToolErrorSamples gathers the sessions' tool error samples, keeping the
// earliest agent.SamplesPerTool of each tool.
func (m *Manifest) ToolErrorSamples() []agent.ToolErrorSample {
	var out []agent.ToolErrorSample
	for _, s := range m.Sessions {
		out = agent.AddSamples(out, s.ToolErrorSamples...)
	}
	return out
}

// Cache is the directory of ingested workspaces.
type Cache struct{ Root string }

// Dir is the entry for key; its workspace is Dir/workspace.
func (c Cache) Dir(key string) string { return filepath.Join(c.Root, key) }

// Workspace is the cached workspace directory for key.
func (c Cache) Workspace(key string) string { return filepath.Join(c.Dir(key), "workspace") }

func (c Cache) manifestPath(key string) string { return filepath.Join(c.Dir(key), "manifest.json") }

// Load returns the manifest for key if the entry is complete and intact.
func (c Cache) Load(key string) (*Manifest, bool, error) {
	b, err := os.ReadFile(c.manifestPath(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false, nil
	}
	if m.Key != key {
		return nil, false, nil
	}
	ok, err := workspace.Matches(c.Workspace(key), m.Files)
	if err != nil || !ok {
		return nil, false, err
	}
	return &m, true, nil
}

// Commit hashes the workspace and writes the manifest — the last write of
// an ingest.
func (c Cache) Commit(m *Manifest) error {
	files, err := workspace.Hash(c.Workspace(m.Key))
	if err != nil {
		return err
	}
	m.Files = files
	return workspace.WriteJSON(c.manifestPath(m.Key), m)
}
