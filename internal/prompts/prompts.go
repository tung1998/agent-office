// Package prompts holds the text office itself sends to models (ADR-122): the
// system prompt's parts, guides, the coordination prompts of Burn and of
// workflows, hand-offs between agents. In English (ADR-121), one Markdown
// file each under area/name.md, embedded in the binary, so they are read and
// changed in one place instead of across the Go code that sends them.
//
// Text gives a file as it is; Render fills it as a text/template with [[ ]]
// delimiters ({{ }} stays literal: guides show the {{payload}} of automations).
package prompts

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"text/template"
)

//go:embed */*.md
var files embed.FS

var (
	mu    sync.Mutex
	cache = map[string]*template.Template{}
)

// Funcs are the helpers a template may use.
var Funcs = template.FuncMap{
	"join":  strings.Join,
	"lines": func(s string) []string { return strings.Split(strings.TrimSpace(s), "\n") },
}

// Text is the prompt name ("area/name", no .md) as written, trimmed. A name
// with no file is a programming error: it panics (TestEveryPromptParses
// reads them all).
func Text(name string) string {
	raw, err := files.ReadFile(name + ".md")
	if err != nil {
		panic(fmt.Sprintf("prompts: no %s.md", name))
	}
	return strings.TrimSpace(string(raw))
}

// Render fills the prompt name with data. A template that fails is a
// programming error too: it panics, tests catch it.
func Render(name string, data any) string {
	t := parsed(name)
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		panic(fmt.Sprintf("prompts: %s: %v", name, err))
	}
	return strings.TrimSpace(b.String())
}

func parsed(name string) *template.Template {
	mu.Lock()
	defer mu.Unlock()
	if t, ok := cache[name]; ok {
		return t
	}
	t := template.Must(template.New(name).Delims("[[", "]]").Funcs(Funcs).Option("missingkey=error").Parse(Text(name)))
	cache[name] = t
	return t
}

// Version returns a short content hash of the prompt's current text, stable
// across runs as long as the .md file is unchanged. Callers that record what
// a job or run used (activity logs, Burn retros) can store this alongside
// the prompt name, so a later behavior change can be traced to a prompt
// edit instead of guessed at from commit history alone.
func Version(name string) string {
	sum := sha256.Sum256([]byte(Text(name)))
	return hex.EncodeToString(sum[:])[:12]
}

// Versions maps every prompt name to its current Version.
func Versions() map[string]string {
	out := map[string]string{}
	for _, n := range Names() {
		out[n] = Version(n)
	}
	return out
}

// Names lists every prompt ("area/name").
func Names() []string {
	var out []string
	dirs, _ := files.ReadDir(".")
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		list, _ := files.ReadDir(d.Name())
		for _, f := range list {
			if strings.HasSuffix(f.Name(), ".md") {
				out = append(out, d.Name()+"/"+strings.TrimSuffix(f.Name(), ".md"))
			}
		}
	}
	return out
}
