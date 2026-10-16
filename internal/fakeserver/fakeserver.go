// Package fakeserver is a minimal DKF MCP server for tests: enough of the
// specification's tools to ingest and answer, plus one read-only and one
// writing extension. Test binaries re-execute themselves as it (see Main), so
// tests need no implementation installed.
package fakeserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

// EnvVar, when set to "1", makes Main serve and exit.
const EnvVar = "PARTICULARS_BENCH_FAKE_SERVER"

// Command is the --impl command template that runs the current test binary
// as the fake server; extra environment variables (e.g. FAKE_VERSION) may be
// passed as "K=V" pairs.
func Command(env ...string) string {
	return strings.Join(append(append([]string{EnvVar + "=1"}, env...), "'"+os.Args[0]+"'", "{workspace}"), " ")
}

// Main serves over stdio and exits when EnvVar is set; otherwise it returns.
// Call it first in TestMain.
func Main() {
	if os.Getenv(EnvVar) != "1" {
		return
	}
	if os.Getenv("FAKE_FAIL") != "" {
		fmt.Fprintln(os.Stderr, "fake server: refusing to start:", os.Getenv("FAKE_FAIL"))
		os.Exit(3)
	}
	ws := os.Args[len(os.Args)-1]
	if err := serve(ws); err != nil {
		fmt.Fprintln(os.Stderr, "fake server:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

type defineIn struct {
	Label string `json:"label" jsonschema:"human-readable label"`
}

type assertIn struct {
	ParticularID string   `json:"particular_id"`
	Content      string   `json:"content"`
	Topics       []string `json:"topics,omitempty"`
	Timestamp    string   `json:"timestamp,omitempty"`
}

type recallIn struct {
	Query string `json:"query,omitempty"`
}

func serve(ws string) error {
	cfg, err := os.ReadFile(filepath.Join(ws, "dkf.yaml"))
	if err != nil {
		return err
	}
	var c struct {
		Workspace struct {
			ID string `yaml:"id"`
		} `yaml:"workspace"`
	}
	if err := yaml.Unmarshal(cfg, &c); err != nil {
		return err
	}
	version := os.Getenv("FAKE_VERSION")
	if version == "" {
		version = "1.0.0"
	}
	instructions := fmt.Sprintf("Fake DKF server bound to %s (id %s). %s", ws, c.Workspace.ID, os.Getenv("FAKE_GUIDANCE"))
	s := mcp.NewServer(&mcp.Implementation{Name: "fake-dkf", Version: version}, &mcp.ServerOptions{Instructions: instructions})

	ro := &mcp.ToolAnnotations{ReadOnlyHint: true}
	rw := &mcp.ToolAnnotations{}
	mcp.AddTool(s, &mcp.Tool{Name: "particular_define", Description: "Define a particular.", Annotations: rw},
		func(ctx context.Context, _ *mcp.CallToolRequest, in defineIn) (*mcp.CallToolResult, any, error) {
			id := "par_" + uuid.Must(uuid.NewV7()).String()
			return text(id), nil, write(ws, "particulars", id, map[string]any{"id": id, "type": "particular", "label": in.Label})
		})
	mcp.AddTool(s, &mcp.Tool{Name: "claim_assert", Description: "Assert a claim.", Annotations: rw},
		func(ctx context.Context, _ *mcp.CallToolRequest, in assertIn) (*mcp.CallToolResult, any, error) {
			id := "clm_" + uuid.Must(uuid.NewV7()).String()
			obj := map[string]any{"id": id, "type": "claim", "subject": in.ParticularID, "content": in.Content,
				"context": map[string]any{"scope": "personal", "topics": in.Topics}}
			if in.Timestamp != "" {
				obj["timestamp"] = in.Timestamp
			}
			return text(id), nil, write(ws, "claims", id, obj)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "knowledge_recall", Description: "Recall claims.", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, in recallIn) (*mcp.CallToolResult, any, error) {
			paths, _ := filepath.Glob(filepath.Join(ws, "claims", "*.yaml"))
			sort.Strings(paths)
			var out []string
			for _, p := range paths {
				b, err := os.ReadFile(p)
				if err != nil {
					return nil, nil, err
				}
				out = append(out, string(b))
			}
			return text(strings.Join(out, "---\n")), nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "topics_list", Description: "List topics (extension).", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return text("[]"), nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "note_write", Description: "Write a note (writing extension).", Annotations: rw},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return text("ok"), nil, os.WriteFile(filepath.Join(ws, "note.txt"), []byte("x"), 0o644)
		})
	return s.Run(context.Background(), &mcp.StdioTransport{})
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func write(ws, dir, id string, obj map[string]any) error {
	if err := os.MkdirAll(filepath.Join(ws, dir), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(obj)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(ws, dir, id+".yaml"), b, 0o644)
}
