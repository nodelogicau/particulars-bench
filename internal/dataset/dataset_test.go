package dataset_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nodelogicau/particulars-bench/internal/dataset"
	"github.com/nodelogicau/particulars-bench/internal/testutil"
)

func TestParse(t *testing.T) {
	qs, err := dataset.Parse(testutil.FixtureJSON(2))
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 10 {
		t.Fatalf("got %d questions, want 10", len(qs))
	}
	q := qs[0]
	if q.Sessions[0].ID != q.ID+"-earlier" || !q.Sessions[0].Date.Before(q.Sessions[1].Date) {
		t.Errorf("sessions not oldest first: %s %v, %s %v", q.Sessions[0].ID, q.Sessions[0].Date, q.Sessions[1].ID, q.Sessions[1].Date)
	}
	if got := q.Sessions[0].Date.Format("2006-01-02T15:04Z07:00"); got != "2023-04-10T09:30Z" {
		t.Errorf("session date %s", got)
	}
	if q.Stratum() != q.Type || q.Abstention() {
		t.Errorf("%s: stratum %s", q.ID, q.Stratum())
	}
	abs := qs[len(qs)-1]
	if !abs.Abstention() || abs.Stratum() != dataset.AbstentionStratum {
		t.Errorf("%s: abstention %t stratum %s", abs.ID, abs.Abstention(), abs.Stratum())
	}
	if abs.Answer != "7" {
		t.Errorf("numeric answer read as %q", abs.Answer)
	}
}

func ids(qs []dataset.Question) []string {
	var out []string
	for _, q := range qs {
		out = append(out, q.ID)
	}
	return out
}

func TestSampleSameSeedSameIDs(t *testing.T) {
	all, err := dataset.Parse(testutil.FixtureJSON(6))
	if err != nil {
		t.Fatal(err)
	}
	sel := dataset.Selection{PerType: 3, Seed: 42}
	a, err := dataset.Sample(all, sel)
	if err != nil {
		t.Fatal(err)
	}
	// File order must not matter.
	reversed := append([]dataset.Question(nil), all...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	b, err := dataset.Sample(reversed, sel)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids(a), ids(b)) {
		t.Errorf("same seed, different ids:\n%v\n%v", ids(a), ids(b))
	}
	if len(a) != 15 {
		t.Errorf("got %d, want 3 per stratum × 5 strata", len(a))
	}
	perStratum := map[string]int{}
	for _, q := range a {
		perStratum[q.Stratum()]++
	}
	if perStratum[dataset.AbstentionStratum] != 3 {
		t.Errorf("abstention stratum: %v", perStratum)
	}
	c, err := dataset.Sample(all, dataset.Selection{PerType: 3, Seed: 43})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(ids(a), ids(c)) {
		t.Errorf("different seeds selected identical ids")
	}
}

func TestSampleNarrowing(t *testing.T) {
	all, err := dataset.Parse(testutil.FixtureJSON(4))
	if err != nil {
		t.Fatal(err)
	}
	got, err := dataset.Sample(all, dataset.Selection{PerType: 2, Seed: 1, Strata: []string{"abstention", "temporal-reasoning"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range got {
		if s := q.Stratum(); s != "abstention" && s != "temporal-reasoning" {
			t.Errorf("unexpected stratum %s", s)
		}
	}
	if len(got) != 4 {
		t.Errorf("got %d", len(got))
	}
	got, err = dataset.Sample(all, dataset.Selection{PerType: 4, Seed: 1, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	strata := map[string]bool{}
	for _, q := range got {
		strata[q.Stratum()] = true
	}
	if len(got) != 5 || len(strata) != 5 {
		t.Errorf("limit 5 should take one per stratum, got %v", ids(got))
	}
	got, err = dataset.Sample(all, dataset.Selection{IDs: []string{"abs-01_abs", "multi-session-03"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids(got), []string{"abs-01_abs", "multi-session-03"}) {
		t.Errorf("ids: %v", ids(got))
	}
	if _, err := dataset.Sample(all, dataset.Selection{IDs: []string{"nope"}}); err == nil {
		t.Error("unknown id accepted")
	}
	if _, err := dataset.Sample(all, dataset.Selection{Strata: []string{"nope"}}); err == nil {
		t.Error("unknown type accepted")
	}
}

func TestEnsureChecksumMismatch(t *testing.T) {
	cache := testutil.InstallFixture(t, 1)
	path := filepath.Join(cache, "longmemeval", "data", "fixture.json")
	if _, _, err := dataset.Ensure("fixture", os.Stderr); err != nil {
		t.Fatalf("intact fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := dataset.Ensure("fixture", os.Stderr)
	var ce *dataset.ChecksumError
	if !errors.As(err, &ce) {
		t.Fatalf("want ChecksumError, got %v", err)
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), dataset.Variants["fixture"].SHA256) {
		t.Errorf("error should name the file and expected checksum: %v", err)
	}
}

func TestPinnedVariants(t *testing.T) {
	for _, name := range []string{"oracle", "s"} {
		f, ok := dataset.Variants[name]
		if !ok || len(f.SHA256) != 64 || !strings.Contains(f.URL, dataset.Revision) {
			t.Errorf("variant %s not pinned: %+v", name, f)
		}
	}
}
