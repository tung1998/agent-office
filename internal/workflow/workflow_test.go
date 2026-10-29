package workflow

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBuiltinsParse(t *testing.T) {
	list, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 3 || list[0].Def.Key != "handoff" || list[1].Def.Key != "advisor" || list[2].Def.Key != "council" {
		t.Fatalf("builtins = %d, first %v", len(list), list[0].Def.Key)
	}
	for _, b := range list {
		if b.Def.Body == "" || len(b.Def.Roles) == 0 {
			t.Errorf("%s: empty body or roles", b.Def.Key)
		}
	}
}

const good = `---
key: thu
name: Thử
description: d
roles:
  - key: a
    access: analyze
  - key: b
    access: edit
    differ_from: [a, dieu-phoi]
parallel: [[a, b]]
brief: [outcome, done_when]
gates:
  - key: ok
    kind: check
vote: { roles: [a, b], quorum: 2, veto: [b] }
---
Làm đi.
`

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"no header":    "chỉ có chữ",
		"unknown":      strings.Replace(good, "name: Thử", "name: Thử\nfoo: 1", 1),
		"bad access":   strings.Replace(good, "access: analyze", "access: root", 1),
		"self differ":  strings.Replace(good, "differ_from: [a, dieu-phoi]", "differ_from: [b]", 1),
		"bad parallel": strings.Replace(good, "[[a, b]]", "[[a, z]]", 1),
		"bad brief":    strings.Replace(good, "[outcome, done_when]", "[outcome, foo]", 1),
		"check kind":   strings.Replace(good, "kind: check", "kind: maybe", 1),
		"quorum":       strings.Replace(good, "quorum: 2", "quorum: 3", 1),
		"no body":      strings.TrimSuffix(good, "Làm đi.\n"),
		"coordinator":  strings.Replace(good, "- key: a\n", "- key: dieu-phoi\n", 1),
	}
	for name, src := range cases {
		if _, err := Parse(src); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestLibrary(t *testing.T) {
	l := Library{Dir: filepath.Join(t.TempDir(), "workflows")}
	list, err := l.List()
	if err != nil || len(list) < 3 {
		t.Fatalf("List defaults = %d, %v", len(list), err)
	}
	if _, err := os.Stat(l.Dir); !os.IsNotExist(err) {
		t.Fatalf("listing builtins created a mutable library directory: %v", err)
	}
	// A pre-existing runtime override is kept on disk but never shadows code.
	legacy := strings.Replace(BuiltinSource("advisor"), "Cố vấn", "Agent override", 1)
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.path("advisor"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	it, err := l.Get("advisor")
	if err != nil || !it.Builtin || it.Source != BuiltinSource("advisor") {
		t.Fatalf("Get builtin = %+v, %v", it, err)
	}
	items, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	advisorCount := 0
	for _, item := range items {
		if item.Def.Key == "advisor" {
			advisorCount++
			if !item.Builtin || item.Source != "" {
				t.Fatalf("listed builtin should be code-backed without duplicating source: %+v", item)
			}
		}
	}
	if advisorCount != 1 {
		t.Fatalf("legacy override created %d advisor entries", advisorCount)
	}
	if _, err := l.Save("advisor", legacy); err != ErrBuiltinImmutable {
		t.Fatalf("Save builtin = %v, want immutable error", err)
	}
	if _, err := l.SaveIf("advisor", legacy, it.Version); err != ErrBuiltinImmutable {
		t.Fatalf("SaveIf builtin = %v, want immutable error", err)
	}
	if err := l.Delete("advisor"); err != ErrBuiltinImmutable {
		t.Fatalf("Delete builtin = %v, want immutable error", err)
	}
	if raw, err := os.ReadFile(l.path("advisor")); err != nil || string(raw) != legacy {
		t.Fatalf("legacy override was changed: %v", err)
	}
	if _, err := l.Save("khac", good); err == nil {
		t.Fatal("key mismatch accepted")
	}
	if _, err := l.Save("thu", good); err != nil {
		t.Fatal(err)
	}
	builtin, err := l.Get("handoff")
	if err != nil || !builtin.Builtin || builtin.Source != BuiltinSource("handoff") {
		t.Fatalf("handoff = %+v, %v", builtin, err)
	}
	if err := os.WriteFile(filepath.Join(l.Dir, "hong.md"), []byte("---\nkey: hong\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err = l.List()
	if err != nil {
		t.Fatal(err)
	}
	var broken bool
	for _, x := range list {
		if x.Def.Key == "hong" && x.Error != "" {
			broken = true
		}
	}
	if !broken {
		t.Fatal("a broken file is not listed with its error")
	}
}

func TestLibrarySaveIf(t *testing.T) {
	l := Library{Dir: filepath.Join(t.TempDir(), "workflows")}
	if _, err := l.Save("thu", good); err != nil {
		t.Fatal(err)
	}
	it, err := l.Get("thu")
	if err != nil {
		t.Fatal(err)
	}
	if it.Version == "" {
		t.Fatal("Get did not set a version")
	}

	// a correct version saves and returns a new one
	other := strings.Replace(good, "Thử", "Thử 2", 1)
	it2, err := l.SaveIf("thu", other, it.Version)
	if err != nil {
		t.Fatalf("SaveIf with current version: %v", err)
	}
	if it2.Version == it.Version {
		t.Fatal("version did not change after a save")
	}

	// the stale version (it.Version) 409s and does not touch the file
	if _, err := l.SaveIf("thu", good, it.Version); err != ErrConflict {
		t.Fatalf("SaveIf with stale version = %v, want ErrConflict", err)
	}
	cur, _ := l.Get("thu")
	if cur.Version != it2.Version {
		t.Fatal("a conflicted save changed the file")
	}

	// no version given skips the check (tools, agents)
	if _, err := l.SaveIf("thu", good, ""); err != nil {
		t.Fatalf("SaveIf with no version: %v", err)
	}

	// a version given for a file that is gone 409s instead of recreating it
	if err := l.Delete("thu"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.SaveIf("thu", good, it2.Version); err != ErrConflict {
		t.Fatalf("SaveIf on a deleted file = %v, want ErrConflict", err)
	}
	if _, err := l.Get("thu"); err != ErrNotFound {
		t.Fatal("a conflicted save over a deleted file recreated it")
	}
}

func TestParseSubWorkflowRole(t *testing.T) {
	d, err := Parse("---\nkey: cha\nname: Cha\nroles:\n  - key: con\n    workflow: advisor\n  - key: ban\n---\nx\n")
	if err != nil {
		t.Fatal(err)
	}
	if d.Roles[0].Access != AccessAgent || d.Roles[1].Access != AccessAgent || d.Limits.Depth != DefaultDepth {
		t.Fatalf("defaults = %+v %+v", d.Roles, d.Limits)
	}
	if _, err := Parse("---\nkey: cha\nname: Cha\nroles:\n  - key: con\n    workflow: Bad Key\nlimits:\n  depth: 9\n---\nx\n"); err == nil || !strings.Contains(err.Error(), "workflow") || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("bad sub-workflow: %v", err)
	}
}

func TestParseContract(t *testing.T) {
	d, err := Parse("---\nkey: a\nname: A\ncallable: sub\ninputs:\n  - key: q\n    required: true\noutputs:\n  - key: r\nroles:\n  - key: x\n    prefer: { tier: Strong, family: OpenAI }\nlimits: { idle: 10m }\n---\nx\n")
	if err != nil {
		t.Fatal(err)
	}
	if d.Roles[0].Prefer.Tier != "strong" || d.Roles[0].Prefer.Family != "openai" || d.Limits.Concurrency != DefaultConcurrency || d.IdleAfter() != 10*time.Minute {
		t.Fatalf("def = %+v", d)
	}
	if _, err := d.RenderInputs(map[string]string{}); err == nil {
		t.Fatal("a required input missing")
	}
	if in, err := d.RenderInputs(map[string]string{"q": "Q?", "extra": "E"}); err != nil || !strings.Contains(in, "Q?") || !strings.Contains(in, "E") {
		t.Fatalf("inputs = %q, %v", in, err)
	}
	if _, err := Parse("---\nkey: a\nname: A\ncallable: nope\nroles:\n  - key: x\n    prefer: { tier: huge }\nlimits: { idle: 5s, concurrency: 99 }\n---\nx\n"); err == nil ||
		!strings.Contains(err.Error(), "callable") || !strings.Contains(err.Error(), "prefer.tier") || !strings.Contains(err.Error(), "idle") || !strings.Contains(err.Error(), "concurrency") {
		t.Fatalf("bad contract: %v", err)
	}
}

func TestOutputTypes(t *testing.T) {
	d, err := Parse("---\nkey: a\nname: A\noutputs:\n  - { key: n, type: number, required: true }\n  - { key: ok, type: boolean }\n  - { key: files, type: list }\n  - { key: raw, type: json }\nroles:\n  - key: x\n---\nx\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.CheckOutputs(map[string]string{"n": "3.5", "ok": "true", "files": `["a.go"]`, "raw": `{"a":1}`}); err != nil {
		t.Fatal(err)
	}
	err = d.CheckOutputs(map[string]string{"n": "ba", "ok": "có", "files": "a.go", "raw": "{"})
	for _, k := range []string{"n (phải là number)", "ok (phải là boolean)", "files (phải là list)", "raw (phải là json)"} {
		if err == nil || !strings.Contains(err.Error(), k) {
			t.Fatalf("missing %q in %v", k, err)
		}
	}
	if _, err := Parse("---\nkey: a\nname: A\noutputs:\n  - { key: n, type: date }\nroles:\n  - key: x\n---\nx\n"); err == nil || !strings.Contains(err.Error(), "type") {
		t.Fatalf("bad type: %v", err)
	}
}

func TestStepsParseRenderEval(t *testing.T) {
	src := "---\nkey: s\nname: S\nroles:\n  - key: a\nsteps:\n  - { id: x, type: agent, role: a, prompt: 'hi {{input.ten}}', next: c }\n  - { id: c, type: condition, if: '{{steps.x.json.n}} > 2', then: e, else: x }\n  - { id: e, type: end }\n---\n"
	d, err := Parse(src)
	if err != nil || !d.StepMode() {
		t.Fatalf("parse: %v", err)
	}
	v := Vars{Input: map[string]string{"ten": "An"}, Steps: map[string]StepResult{"x": NewResult(`{"n": 3, "l": ["p", "q"]}`, "")}}
	if Render(d.Steps[0].Prompt, v) != "hi An" || Render("{{steps.x.json.l.1}}|{{steps.nope.output}}", v) != "q|" {
		t.Fatal("render")
	}
	for expr, want := range map[string]bool{"{{steps.x.json.n}} > 2": true, "{{steps.x.json.n}} == 3.0": true, "{{input.ten}} contains A": true, "{{input.ten}} != An": false, "{{steps.nope.output}}": false, "{{input.ten}}": true} {
		if Eval(expr, v) != want {
			t.Fatalf("eval %q", expr)
		}
	}
	// the canvas writes the file back from the definition: it parses the same
	out, err := Format(d)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := Parse(out); err != nil || len(again.Steps) != 3 || !slices.Equal(again.Steps[1].Else, Targets{"x"}) {
		t.Fatalf("format round trip: %v\n%s", err, out)
	}
	bad := "---\nkey: s\nname: S\nsteps:\n  - { id: x, type: agent, role: nope }\n  - { id: h, type: http, url: ftp://x, next: zz }\n  - { id: k, type: code, lang: ruby }\n  - { id: w, type: wait, for: 48h, next: x }\n---\n"
	_, err = Parse(bad)
	for _, want := range []string{`role "nope"`, "thiếu prompt", "url", `next "zz"`, "lang", "thiếu script", `for "48h"`, "bước end"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q in %v", want, err)
		}
	}
}

func TestParseCall(t *testing.T) {
	for _, c := range []struct {
		in, key, rest string
		ok            bool
	}{
		{"#review fix the bug", "review", "fix the bug", true},
		{"#review", "review", "", true},
		{"# heading", "", "# heading", false},
		{"/review x", "", "/review x", false},
		{"see #review", "", "see #review", false},
	} {
		k, r, ok := ParseCall(c.in)
		if k != c.key || r != c.rest || ok != c.ok {
			t.Errorf("ParseCall(%q) = %q %q %v", c.in, k, r, ok)
		}
	}
	if Call("review", " x ") != "#review x" || Call("review", "") != "#review" {
		t.Error("Call")
	}
}

// ADR-185: an agent step may carry a skill; the website workflow gives each
// of its five agent steps one.
func TestStepSkill(t *testing.T) {
	def, err := Parse(BuiltinSource("website"))
	if err != nil {
		t.Fatal(err)
	}
	var skills []string
	for _, s := range def.Steps {
		if s.Skill != "" {
			skills = append(skills, s.Skill)
		}
	}
	if strings.Join(skills, ",") != "website-blueprint,homepage-copy,page-builder,design-polish,launch-checklist" {
		t.Fatalf("skills = %v", skills)
	}
	def.Steps[0].Skill = "bad name"
	if err := def.Validate(); err == nil || !strings.Contains(err.Error(), "tên skill") {
		t.Fatalf("bad skill name: %v", err)
	}
}
