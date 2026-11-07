// Package textrank ranks short texts by the words of a query, accents
// ignored (ADR-235): recall over the agents' notes, the wiki search.
package textrank

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

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

// abbrev expands common Vietnamese typing abbreviations to their full word,
// matched whole (never inside a longer word, so terms like "vd1" or variable
// names keep their letters) before folding, so a note written "không có" is
// found by a query typed "ko có".
var abbrev = map[string]string{
	"ko":  "không",
	"k":   "không",
	"dc":  "được",
	"đc":  "được",
	"vd":  "ví dụ",
	"vs":  "với",
	"ng":  "người",
	"nx":  "nữa",
	"trc": "trước",
	"bt":  "bình thường",
}

// isWordRune is a letter or digit: what makes up the abbreviation itself,
// as opposed to punctuation stuck to it ("vd:", "(ko", "ko,").
func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// splitPunct splits word into leading punctuation, a letter/digit core, and
// trailing punctuation ("(ko," -> "(", "ko", ",").
func splitPunct(word string) (lead, core, trail string) {
	i := strings.IndexFunc(word, isWordRune)
	if i < 0 {
		return word, "", ""
	}
	j := strings.LastIndexFunc(word, isWordRune)
	_, size := utf8.DecodeRuneInString(word[j:])
	j += size
	return word[:i], word[i:j], word[j:]
}

// Expand returns the full word for a known Vietnamese abbreviation (matched
// whole, case-insensitively, ignoring punctuation stuck to it such as
// "vd:" or "(ko"), or word unchanged if it isn't one.
func Expand(word string) string {
	lead, core, trail := splitPunct(word)
	if full, ok := abbrev[strings.ToLower(core)]; ok {
		return lead + full + trail
	}
	return word
}

// reverseAbbrev maps a full phrase (lowercase, however many words) to the
// abbreviations that expand to it, for the full->abbreviation direction
// (searching "ví dụ" should also find a message that only says "vd").
var reverseAbbrev = func() map[string][]string {
	m := map[string][]string{}
	for short, full := range abbrev {
		full = strings.ToLower(full)
		m[full] = append(m[full], short)
	}
	for _, shorts := range m {
		slices.Sort(shorts)
	}
	return m
}()

// FullPhraseWords is how many words the longest known full phrase has
// (e.g. "ví dụ" is 2), so callers know how far to look ahead.
var FullPhraseWords = func() int {
	n := 1
	for full := range reverseAbbrev {
		if c := len(strings.Fields(full)); c > n {
			n = c
		}
	}
	return n
}()

// ReverseExpand returns the known abbreviations for a full phrase (matched
// whole, case-insensitively, any number of words), or nil if phrase isn't
// the expansion of one.
func ReverseExpand(phrase string) []string {
	return reverseAbbrev[strings.ToLower(phrase)]
}

// Normalize expands each word's abbreviation (if any) then folds, so a
// query and a doc written with different abbreviations of the same word
// still meet in the middle. Used on both sides of Rank: the query (via
// Words) and the docs it ranks against.
func Normalize(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = Expand(w)
	}
	return Fold(strings.Join(words, " "))
}

// Words is the query's words, normalized, each once.
func Words(query string) []string {
	var out []string
	for _, w := range strings.Fields(Normalize(query)) {
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
