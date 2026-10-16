package dataset

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
)

// Selection narrows the dataset to the questions a run uses.
type Selection struct {
	PerType int    `json:"per_type"`
	Seed    uint64 `json:"seed"`
	// Strata, when non-empty, keeps only these strata (question types or
	// "abstention").
	Strata []string `json:"types,omitempty"`
	// IDs, when non-empty, selects exactly these questions; sampling and
	// strata are not applied.
	IDs []string `json:"ids,omitempty"`
	// Limit, when positive, caps the total, taking strata round-robin.
	Limit int `json:"limit,omitempty"`
}

// Sample selects questions: a seeded, stratified sample of PerType questions
// per stratum, abstention questions forming their own stratum. The order of
// the result is by stratum, then by sample rank. The same dataset, seed, and
// selection always yield the same questions.
func Sample(all []Question, sel Selection) ([]Question, error) {
	if len(sel.IDs) > 0 {
		byID := make(map[string]Question, len(all))
		for _, q := range all {
			byID[q.ID] = q
		}
		var out []Question
		for _, id := range sel.IDs {
			q, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("question %q is not in the dataset", id)
			}
			out = append(out, q)
		}
		return limit(out, sel.Limit), nil
	}

	strata := map[string][]Question{}
	for _, q := range all {
		strata[q.Stratum()] = append(strata[q.Stratum()], q)
	}
	names := make([]string, 0, len(strata))
	for n := range strata {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(sel.Strata) > 0 {
		var keep []string
		for _, want := range sel.Strata {
			if _, ok := strata[want]; !ok {
				return nil, fmt.Errorf("unknown question type %q (known: %v)", want, names)
			}
			keep = append(keep, want)
		}
		sort.Strings(keep)
		names = keep
	}

	// Rank each question by a hash of the seed and its id: independent of
	// file order and of the Go version's random number generator.
	var picked [][]Question
	for _, n := range names {
		qs := append([]Question(nil), strata[n]...)
		sort.Slice(qs, func(i, j int) bool {
			ri, rj := rank(sel.Seed, qs[i].ID), rank(sel.Seed, qs[j].ID)
			if ri != rj {
				return ri < rj
			}
			return qs[i].ID < qs[j].ID
		})
		if sel.PerType > 0 && len(qs) > sel.PerType {
			qs = qs[:sel.PerType]
		}
		picked = append(picked, qs)
	}

	var out []Question
	if sel.Limit > 0 {
		for i := 0; len(out) < sel.Limit; i++ {
			added := false
			for _, qs := range picked {
				if i < len(qs) && len(out) < sel.Limit {
					out = append(out, qs[i])
					added = true
				}
			}
			if !added {
				break
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Stratum() < out[j].Stratum() })
		return out, nil
	}
	for _, qs := range picked {
		out = append(out, qs...)
	}
	return out, nil
}

func limit(qs []Question, n int) []Question {
	if n > 0 && len(qs) > n {
		return qs[:n]
	}
	return qs
}

func rank(seed uint64, id string) uint64 {
	h := sha256.Sum256([]byte(strconv.FormatUint(seed, 10) + "\x00" + id))
	return binary.BigEndian.Uint64(h[:8])
}
