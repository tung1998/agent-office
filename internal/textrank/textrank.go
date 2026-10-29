// Package textrank ranks short texts by the words of a query, accents
// ignored (ADR-235): recall over the agents' notes, the wiki search.
package textrank

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Fold lowercases and drops diacritics so "Đơn hàng" matches "don hang".
func Fold(s string) string {
	s = strings.NewReplacer("đ", "d", "Đ", "d").Replace(strings.ToLower(s))
	out, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), s)
	if err != nil {
		return s
	}
	return out
}

// Words is the query's words, folded, each once.
func Words(query string) []string {
	var out []string
	for _, w := range strings.Fields(Fold(query)) {
		if !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

// Hit is one ranked text: its place in docs, how many words it has, and
// its score (the words it has, a rare one weighing more: idf).
type Hit struct {
	At, Matched int
	Score       float64
}

// Rank is the docs (already folded) with every word, else, when none has
// them all, those with at least half of them: more words first, then the
// higher score, then the later doc. full is how many have every word.
func Rank(docs, words []string) (hits []Hit, full int) {
	if len(words) == 0 {
		return nil, 0
	}
	idf := make([]float64, len(words))
	for i, w := range words {
		df := 0
		for _, d := range docs {
			if strings.Contains(d, w) {
				df++
			}
		}
		idf[i] = math.Log(1 + float64(len(docs)+1)/float64(df+1))
	}
	for i, d := range docs {
		h := Hit{At: i}
		for j, w := range words {
			if strings.Contains(d, w) {
				h.Matched++
				h.Score += idf[j]
			}
		}
		if h.Matched == len(words) {
			full++
		} else if h.Matched == 0 || 2*h.Matched < len(words) {
			continue
		}
		hits = append(hits, h)
	}
	if full > 0 {
		hits = slices.DeleteFunc(hits, func(h Hit) bool { return h.Matched < len(words) })
	}
	slices.SortStableFunc(hits, func(a, b Hit) int {
		if c := cmp.Compare(b.Matched, a.Matched); c != 0 {
			return c
		}
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return b.At - a.At
	})
	return hits, full
}
