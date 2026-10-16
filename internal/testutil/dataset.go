package testutil

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nodelogicau/particulars-bench/internal/dataset"
)

// FixtureTypes are the question types in the fixture dataset.
var FixtureTypes = []string{"knowledge-update", "multi-session", "single-session-user", "temporal-reasoning"}

// FixtureJSON returns a small LongMemEval-shaped dataset: perType questions
// of each FixtureTypes entry plus perType abstention questions, each with two
// sessions listed newest first.
func FixtureJSON(perType int) []byte {
	var qs []map[string]any
	add := func(id, typ string, answer any) {
		qs = append(qs, map[string]any{
			"question_id":          id,
			"question_type":        typ,
			"question":             "What colour is my bike? (" + id + ")",
			"answer":               answer,
			"question_date":        "2023/06/01 (Thu) 12:00",
			"haystack_dates":       []string{"2023/05/01 (Mon) 10:00", "2023/04/10 (Mon) 09:30"},
			"haystack_session_ids": []string{id + "-later", id + "-earlier"},
			"haystack_sessions": [][]map[string]any{
				{{"role": "user", "content": "I repainted my bike red.", "has_answer": true}, {"role": "assistant", "content": "Nice."}},
				{{"role": "user", "content": "I bought a blue bike."}, {"role": "assistant", "content": "Enjoy it."}},
			},
			"answer_session_ids": []string{id + "-later"},
		})
	}
	for _, typ := range FixtureTypes {
		for i := 0; i < perType; i++ {
			add(fmt.Sprintf("%s-%02d", typ, i), typ, "red")
		}
	}
	for i := 0; i < perType; i++ {
		add(fmt.Sprintf("abs-%02d_abs", i), "multi-session", 7)
	}
	b, err := json.Marshal(qs)
	if err != nil {
		panic(err)
	}
	return b
}

// InstallFixture points the cache at a temporary directory, writes the
// fixture there as the dataset variant "fixture", and pins its checksum.
func InstallFixture(t *testing.T, perType int) string {
	t.Helper()
	cache := t.TempDir()
	t.Setenv("PARTICULARS_BENCH_CACHE", cache)
	b := FixtureJSON(perType)
	sum := sha256.Sum256(b)
	f := dataset.File{Name: "fixture.json", SHA256: hex.EncodeToString(sum[:]), URL: "http://invalid.example/fixture.json"}
	dir := filepath.Join(cache, "longmemeval", "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, f.Name), b, 0o644); err != nil {
		t.Fatal(err)
	}
	dataset.Variants["fixture"] = f
	t.Cleanup(func() { delete(dataset.Variants, "fixture") })
	return cache
}
