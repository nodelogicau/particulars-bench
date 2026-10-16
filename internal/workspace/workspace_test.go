package workspace_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nodelogicau/particulars-bench/internal/spec"
	"github.com/nodelogicau/particulars-bench/internal/workspace"
)

func TestCreate(t *testing.T) {
	dir := t.TempDir()
	id, err := workspace.Create(dir)
	if err != nil {
		t.Fatal(err)
	}
	format, gotID, err := workspace.ReadConfig(dir)
	if err != nil || format != spec.DKFFormat || gotID != id {
		t.Fatalf("format %q id %q (want %q): %v", format, gotID, id, err)
	}
	// workspace.id is a bare lowercase UUIDv7.
	if len(id) != 36 || id[14] != '7' || strings.ToLower(id) != id {
		t.Errorf("id %q is not a lowercase UUIDv7", id)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "dkf.yaml"))
	for _, want := range []string{"scope: personal", "author: user", "harness: longmemeval-bench"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("dkf.yaml lacks %q:\n%s", want, b)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("Create wrote more than dkf.yaml: %v", entries)
	}
	if _, err := workspace.Create(dir); err == nil {
		t.Error("Create accepted a non-empty directory")
	}
}

func TestHashMatchesCopy(t *testing.T) {
	src := t.TempDir()
	if _, err := workspace.Create(src); err != nil {
		t.Fatal(err)
	}
	write(t, src, "claims/a.yaml", "id: a\n")
	files, err := workspace.Hash(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy")
	if err := workspace.Copy(src, dst); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{src, dst} {
		if ok, err := workspace.Matches(dir, files); !ok || err != nil {
			t.Errorf("%s does not match: %v", dir, err)
		}
	}
	write(t, dst, "claims/b.yaml", "id: b\n")
	if ok, _ := workspace.Matches(dst, files); ok {
		t.Error("extra file not detected")
	}
	write(t, src, "claims/a.yaml", "id: changed\n")
	if ok, _ := workspace.Matches(src, files); ok {
		t.Error("changed file not detected")
	}
	if ok, err := workspace.Matches(filepath.Join(src, "missing"), files); ok || err != nil {
		t.Errorf("missing directory: %t %v", ok, err)
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func claim(id, ts string, topics ...string) string {
	s := "id: " + id + "\ntype: claim\nsubject: par_1\ncontent: x\ncontext:\n  scope: personal\n"
	if len(topics) > 0 {
		s += "  topics:\n"
		for _, tp := range topics {
			s += "    - " + tp + "\n"
		}
	}
	return s + "timestamp: " + ts + "\n"
}

func TestMetricsFlat(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "dkf.yaml", "format: dkf/0.1\nworkspace:\n  id: x\n")
	write(t, dir, "particulars/par_1.yaml", "id: par_1\ntype: particular\nlabel: The user\n")
	write(t, dir, "claims/clm_1.yaml", claim("clm_1", "2023-04-10T09:30:00Z", "bikes"))
	write(t, dir, "claims/clm_2.yaml", claim("clm_2", "2026-10-16T10:00:00Z", "bikes", "colour"))
	write(t, dir, "claims/clm_3.yaml", claim("clm_3", "2023-05-01T10:00:00Z")+"retracted:\n  reason: wrong\n")
	write(t, dir, "syntheses/syn_1.yaml", "id: syn_1\ntype: synthesis\ncontext:\n  topics:\n    - travel\n")
	m, err := workspace.ComputeMetrics(dir, map[string]string{"clm_1": "2023-04-10", "clm_2": "2023-04-10", "clm_3": "2023-05-01"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Claims != 3 || m.RetractedClaims != 1 || m.Particulars != 1 || m.Syntheses != 1 || m.Topics != 3 {
		t.Errorf("metrics %+v", m)
	}
	if m.OnSessionDate == nil || *m.OnSessionDate < 0.66 || *m.OnSessionDate > 0.67 {
		t.Errorf("on session date %v, want 2/3", m.OnSessionDate)
	}
}

func TestMetricsSharded(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "dkf.yaml", "format: dkf/0.2\nworkspace:\n  id: x\n")
	write(t, dir, "claims/2026-10/clm_1.yaml", claim("clm_1", "2023-04-10T09:30:00Z"))
	write(t, dir, "claims/legacy/clm_2.yaml", claim("clm_2", "2023-04-10T09:30:00Z"))
	write(t, dir, "claims/clm_stray.yaml", claim("clm_stray", "2023-04-10T09:30:00Z"))
	m, err := workspace.ComputeMetrics(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Claims != 2 || m.OnSessionDate != nil {
		t.Errorf("sharded metrics %+v (a stray top-level file is not an object)", m)
	}
}
