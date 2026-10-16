package impl_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/fakeserver"
	"github.com/nodelogicau/particulars-bench/internal/impl"
	"github.com/nodelogicau/particulars-bench/internal/spec"
	"github.com/nodelogicau/particulars-bench/internal/workspace"
)

func TestMain(m *testing.M) {
	fakeserver.Main()
	os.Exit(m.Run())
}

func TestParse(t *testing.T) {
	im, err := impl.Parse("cli=particulars serve --mcp --workspace {workspace}")
	if err != nil || im.Label != "cli" || im.Command != "particulars serve --mcp --workspace {workspace}" {
		t.Fatalf("got %+v, %v", im, err)
	}
	for _, bad := range []string{"cli", "=x {workspace}", "cli=particulars serve"} {
		if _, err := impl.Parse(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func newWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := workspace.Create(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func names(tools []*mcp.Tool) []string {
	var out []string
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

func TestStartFakeServer(t *testing.T) {
	ws := newWorkspace(t)
	srv, err := impl.Start(context.Background(), impl.Impl{Label: "fake", Command: fakeserver.Command("FAKE_VERSION=2.3.4")}, ws)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	if srv.Info != (impl.ServerInfo{Name: "fake-dkf", Version: "2.3.4"}) {
		t.Errorf("serverInfo %+v", srv.Info)
	}
	if !strings.Contains(srv.Instructions, srv.Workspace) {
		t.Errorf("{workspace} not substituted with %s: %q", srv.Workspace, srv.Instructions)
	}
	out, isErr, err := srv.Call(context.Background(), "claim_assert", json.RawMessage(`{"particular_id":"p","content":"c"}`))
	if err != nil || isErr || !strings.HasPrefix(out, "clm_") {
		t.Fatalf("claim_assert: %q %t %v", out, isErr, err)
	}
}

func TestStartFailureKeepsStderr(t *testing.T) {
	ws := newWorkspace(t)
	_, err := impl.Start(context.Background(), impl.Impl{Label: "broken", Command: fakeserver.Command("FAKE_FAIL=no-licence")}, ws)
	var se *impl.StartError
	if !errors.As(err, &se) {
		t.Fatalf("want StartError, got %v", err)
	}
	if !strings.Contains(se.Stderr, "no-licence") || !strings.Contains(err.Error(), "no-licence") {
		t.Errorf("stderr not captured: %q", se.Stderr)
	}
}

func TestAnswerToolsFilter(t *testing.T) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true}
	tools := []*mcp.Tool{
		{Name: "claim_assert", Annotations: ro}, // a spec write tool, whatever it claims
		{Name: "synthesis_create"},
		{Name: "knowledge_recall"}, // spec query tool, offered without annotations
		{Name: "particular_resolve", Annotations: ro},
		{Name: "topics_list", Annotations: ro},
		{Name: "note_write"},
	}
	offered, ext := impl.AnswerTools(tools, false)
	if got := names(offered); !reflect.DeepEqual(got, []string{"knowledge_recall", "particular_resolve", "topics_list"}) {
		t.Errorf("offered %v", got)
	}
	if !reflect.DeepEqual(ext, []string{"topics_list"}) {
		t.Errorf("extensions %v", ext)
	}
	offered, ext = impl.AnswerTools(tools, true)
	if got := names(offered); !reflect.DeepEqual(got, []string{"knowledge_recall", "particular_resolve"}) || len(ext) != 0 {
		t.Errorf("spec-tools-only offered %v, extensions %v", got, ext)
	}
}

// assertVerbatim checks that every tool renders, as a Messages API tool and
// as an OpenAI function, with the server's name, description, and input
// schema, and with strict mode off.
func assertVerbatim(t *testing.T, tools []*mcp.Tool) {
	t.Helper()
	for _, tool := range tools {
		def, err := impl.ToolDef(tool)
		if err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		want, err := impl.SchemaMap(tool)
		if err != nil {
			t.Fatal(err)
		}
		at, err := agent.AnthropicTool(def)
		if err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		var anth struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"input_schema"`
		}
		roundTrip(t, at, &anth)
		var fn struct {
			Type     string `json:"type"`
			Function struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				Parameters  map[string]any `json:"parameters"`
				Strict      *bool          `json:"strict"`
			} `json:"function"`
		}
		roundTrip(t, agent.OpenAITool(def), &fn)
		for _, got := range []struct {
			format, name, desc string
			schema             map[string]any
		}{
			{"anthropic", anth.Name, anth.Description, anth.InputSchema},
			{"openai", fn.Function.Name, fn.Function.Description, fn.Function.Parameters},
		} {
			if got.name != tool.Name || got.desc != tool.Description {
				t.Errorf("%s %s: name/description changed: %q %q", got.format, tool.Name, got.name, got.desc)
			}
			if !reflect.DeepEqual(got.schema, want) {
				t.Errorf("%s %s: schema changed\n got %v\nwant %v", got.format, tool.Name, got.schema, want)
			}
		}
		if fn.Type != "function" || fn.Function.Strict != nil {
			t.Errorf("openai %s: type %q, strict %v", tool.Name, fn.Type, fn.Function.Strict)
		}
	}
}

func roundTrip(t *testing.T, v, out any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
}

func TestToolsVerbatimFake(t *testing.T) {
	srv, err := impl.Start(context.Background(), impl.Impl{Label: "fake", Command: fakeserver.Command()}, newWorkspace(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	assertVerbatim(t, srv.Tools)
}

// TestReferenceCLI runs against the reference implementation when
// `particulars` is on PATH.
func TestReferenceCLI(t *testing.T) {
	if _, err := exec.LookPath("particulars"); err != nil {
		t.Skip("particulars not on PATH")
	}
	ws := newWorkspace(t)
	srv, err := impl.Start(context.Background(), impl.Impl{Label: "cli", Command: "particulars serve --mcp --workspace {workspace}"}, ws)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	if srv.Info.Name != "particulars" || srv.Info.Version == "" {
		t.Errorf("serverInfo %+v", srv.Info)
	}
	if srv.Instructions == "" {
		t.Error("no initialize instructions")
	}
	assertVerbatim(t, srv.Tools)

	offered, ext := impl.AnswerTools(srv.Tools, false)
	got := names(offered)
	for _, q := range spec.QueryTools {
		if !contains(got, q) {
			t.Errorf("spec query tool %s not offered", q)
		}
	}
	for _, w := range []string{"claim_assert", "synthesis_create", "particular_define", "claim_retract"} {
		if contains(got, w) {
			t.Errorf("write tool %s offered", w)
		}
	}
	if !contains(ext, "topics_list") {
		t.Errorf("read-only extension topics_list not offered: %v", ext)
	}
	offered, _ = impl.AnswerTools(srv.Tools, true)
	if contains(names(offered), "topics_list") {
		t.Error("--spec-tools-only offered topics_list")
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
