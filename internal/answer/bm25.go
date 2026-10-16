package answer

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// BM25 parameters (Robertson/Spärck Jones defaults).
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// Tokenize lowercases text and splits it on anything that is not a letter or
// digit.
func Tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// BM25TopK returns the indexes of the k documents scoring highest against
// query, best first, ties broken by index. Only documents sharing a term
// with the query score above zero; fewer than k may be returned. No model
// or embedding service is involved.
func BM25TopK(docs []string, query string, k int) []int {
	n := len(docs)
	if n == 0 || k <= 0 {
		return nil
	}
	tf := make([]map[string]int, n)
	df := map[string]int{}
	lengths := make([]float64, n)
	var total float64
	for i, d := range docs {
		toks := Tokenize(d)
		lengths[i] = float64(len(toks))
		total += lengths[i]
		tf[i] = map[string]int{}
		for _, t := range toks {
			tf[i][t]++
		}
		for t := range tf[i] {
			df[t]++
		}
	}
	avg := total / float64(n)
	if avg == 0 {
		avg = 1
	}
	qterms := map[string]bool{}
	for _, t := range Tokenize(query) {
		qterms[t] = true
	}
	type scored struct {
		i int
		s float64
	}
	var hits []scored
	for i := range docs {
		var s float64
		for t := range qterms {
			f := float64(tf[i][t])
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (float64(n)-float64(df[t])+0.5)/(float64(df[t])+0.5))
			s += idf * f * (bm25K1 + 1) / (f + bm25K1*(1-bm25B+bm25B*lengths[i]/avg))
		}
		if s > 0 {
			hits = append(hits, scored{i, s})
		}
	}
	sort.Slice(hits, func(a, b int) bool {
		if hits[a].s != hits[b].s {
			return hits[a].s > hits[b].s
		}
		return hits[a].i < hits[b].i
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	out := make([]int, len(hits))
	for j, h := range hits {
		out[j] = h.i
	}
	return out
}
