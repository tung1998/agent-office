// Package wiki searches a project's wiki (docs/wiki/, ADR-220): markdown
// pages split into sections, ranked by the words of a query like the notes
// (ADR-235), so recall finds what the wiki knows without reading it all.
package wiki

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/textrank"
)

// Dir is where a project keeps its wiki.
const Dir = "docs/wiki"

const (
	maxFiles   = 500
	maxFile    = 256 << 10 // a bigger page is skipped
	maxSection = 1500      // a longer section is cut into parts by paragraph
	snippet    = 400       // runes of a hit shown
)

// Hit is one section of a page: where it is and its start.
type Hit struct {
	Path, Heading, Text string
}

type section struct {
	path, heading, text string
}

// Search is the sections of root's wiki nearest query, at most n; none when
// the project has no wiki.
func Search(root, query string, n int) ([]Hit, error) {
	words := textrank.Words(query)
	if root == "" || len(words) == 0 {
		return nil, nil
	}
	secs, err := sections(root)
	if err != nil || len(secs) == 0 {
		return nil, err
	}
	docs := make([]string, len(secs))
	for i, s := range secs {
		docs[i] = textrank.Fold(s.path + " " + s.heading + " " + s.text)
	}
	ranked, _ := textrank.Rank(docs, words)
	var out []Hit
	for _, h := range ranked[:min(n, len(ranked))] {
		s := secs[h.At]
		out = append(out, Hit{Path: s.path, Heading: s.heading, Text: short(s.text)})
	}
	return out, nil
}

func sections(root string) ([]section, error) {
	dir := filepath.Join(root, Dir)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, nil
	}
	var out []section
	files := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if files >= maxFiles {
			return fs.SkipAll
		}
		if err != nil {
			return nil // unreadable: skipped
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > maxFile || !info.Mode().IsRegular() {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		files++
		rel, _ := filepath.Rel(root, p)
		out = append(out, split(filepath.ToSlash(rel), string(raw))...)
		return nil
	})
	return out, err
}

// split cuts a page at its headings, a long section again by paragraph.
func split(path, page string) []section {
	var out []section
	heading := ""
	var body strings.Builder
	flush := func() {
		text := strings.TrimSpace(body.String())
		body.Reset()
		for text != "" {
			part := text
			if len(part) > maxSection {
				cut := strings.LastIndex(part[:maxSection], "\n\n")
				if cut <= 0 {
					cut = maxSection
					for cut > 0 && !utf8.RuneStart(part[cut]) {
						cut--
					}
				}
				part = part[:cut]
			}
			out = append(out, section{path: path, heading: heading, text: strings.TrimSpace(part)})
			text = strings.TrimSpace(text[len(part):])
		}
	}
	for _, line := range strings.Split(page, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "#") {
			flush()
			heading = strings.TrimSpace(strings.TrimLeft(t, "#"))
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	flush()
	return out
}

func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > snippet {
		s = string([]rune(s)[:snippet-1]) + "…"
	}
	return s
}
