package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Steps (ADR-108): a workflow may be a graph of steps the office runs in
// order, as n8n does, instead of a coordinator deciding: an agent's turn, a
// sub-workflow, code, an HTTP request, a condition (and loops by going back),
// a wait, a person's approval, a check command, the end. Data goes from step to
// step by templates: {{input.key}}, {{steps.<id>.output}},
// {{steps.<id>.json.a.b}}, {{steps.<id>.status}}.
//
// One output may go to several steps (next: [a, b]): they run at once; a
// step several branches reach waits for every branch that can still reach
// it, then runs once (a join). A switch sends its output to the case its
// value matches. A step that failed still has an output (status "error")
// for the steps after it when on_error is continue.

// Step types.
const (
	StepAgent = "agent"
	// StepCoordinate: an agent decides inside the step (ADR-111): it hands
	// work to the step's roles, asks again, votes, passes the gates, as a
	// workflow without steps does; workflow_done is the step's output.
	StepCoordinate = "coordinate"
	StepWorkflow   = "workflow"
	StepCode       = "code"
	StepHTTP       = "http"
	StepCondition  = "condition"
	StepSwitch     = "switch"
	// StepWait: the run waits For (30m, 1h…) before its next steps, as a
	// check "an hour later" (ADR-237); an office restart ends the run.
	StepWait    = "wait"
	StepApprove = "approve"
	StepCheck   = "check"
	StepEnd     = "end"
)

// StepTypes are the step types, in the order the editor offers them.
var StepTypes = []string{StepAgent, StepCoordinate, StepWorkflow, StepCode, StepHTTP, StepCondition, StepSwitch, StepWait, StepApprove, StepCheck, StepEnd}

// Step is one node of a workflow's graph.
type Step struct {
	ID   string `yaml:"id" json:"id"`
	Type string `yaml:"type" json:"type"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Next are the steps after this one, run at once (approve: once
	// approved; condition: Then/Else; switch: its Cases, Else by default)
	Next Targets `yaml:"next,omitempty" json:"next,omitempty"`
	// OnError: "stop" (default) ends the run when the step fails; "continue" goes on to Next
	OnError string `yaml:"on_error,omitempty" json:"on_error,omitempty"`

	// agent: a role of the workflow (the project binds it to an agent) and what it is asked
	// coordinate: Role (optional) is the role whose agent coordinates (none:
	// the run's coordinator), Prompt its instructions, Roles the roles it
	// hands work to (none: every role but Role)
	Role   string   `yaml:"role,omitempty" json:"role,omitempty"`
	Prompt string   `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	Roles  []string `yaml:"roles,omitempty" json:"roles,omitempty"`
	// Skill (agent): a project skill whose instructions go with the prompt (ADR-185)
	Skill string `yaml:"skill,omitempty" json:"skill,omitempty"`
	// workflow: another workflow of the project, given inputs by key
	Workflow string            `yaml:"workflow,omitempty" json:"workflow,omitempty"`
	Inputs   map[string]string `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	// code: a script (bash | node | python) run in the project's folder; it
	// gets the run's data as JSON on stdin and in OFFICE_* variables
	Lang     string `yaml:"lang,omitempty" json:"lang,omitempty"`
	Script   string `yaml:"script,omitempty" json:"script,omitempty"`
	TimeoutS int    `yaml:"timeout_s,omitempty" json:"timeout_s,omitempty"`
	// http: a request; the response body is its output (and json when it is JSON)
	Method  string            `yaml:"method,omitempty" json:"method,omitempty"`
	URL     string            `yaml:"url,omitempty" json:"url,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body    string            `yaml:"body,omitempty" json:"body,omitempty"`
	// condition: If (see Eval) chooses Then or Else. Going back to an
	// earlier step is a loop: any step runs at most MaxLoops+1 times (default 5)
	If       string  `yaml:"if,omitempty" json:"if,omitempty"`
	Then     Targets `yaml:"then,omitempty" json:"then,omitempty"`
	Else     Targets `yaml:"else,omitempty" json:"else,omitempty"`
	MaxLoops int     `yaml:"max_loops,omitempty" json:"max_loops,omitempty"`
	// switch: Value (a template) goes to the case it equals (no case: Else)
	Value string `yaml:"value,omitempty" json:"value,omitempty"`
	Cases []Case `yaml:"cases,omitempty" json:"cases,omitempty"`
	// wait: how long, a Go duration (30m, 1h30m) or a template, at most MaxWait
	For string `yaml:"for,omitempty" json:"for,omitempty"`
	// approve: what the person decides (rejected: Else, or the run stops)
	Note string `yaml:"note,omitempty" json:"note,omitempty"`
	// check: a command that must pass (the project's command rules apply)
	Command string `yaml:"command,omitempty" json:"command,omitempty"`
	// end: the run's summary and outputs (templates)
	Summary string            `yaml:"summary,omitempty" json:"summary,omitempty"`
	Outputs map[string]string `yaml:"outputs,omitempty" json:"outputs,omitempty"`
	// Position on the editor's canvas (the office does not read it)
	Position *Position `yaml:"position,omitempty" json:"position,omitempty"`
}

// Case is one way out of a switch: the value it matches (case aside) and
// the steps it goes to.
type Case struct {
	When string  `yaml:"when" json:"when"`
	Next Targets `yaml:"next" json:"next"`
}

// Targets are the steps an output goes to: one id, or a list in the file.
type Targets []string

// UnmarshalYAML reads "a" or [a, b].
func (t *Targets) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*t = nil
		if v := strings.TrimSpace(n.Value); v != "" {
			*t = Targets{v}
		}
		return nil
	}
	var list []string
	if err := n.Decode(&list); err != nil {
		return err
	}
	*t = Targets(list)
	return nil
}

// MarshalYAML writes one step as "a", several as a list.
func (t Targets) MarshalYAML() (any, error) {
	if len(t) == 1 {
		return t[0], nil
	}
	return []string(t), nil
}

// UnmarshalJSON reads "a" or ["a", "b"] (the editor sends a list).
func (t *Targets) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*t = nil
		if one != "" {
			*t = Targets{one}
		}
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	*t = Targets(list)
	return nil
}

// Outs are every step a step may go to.
func (s Step) Outs() []string {
	out := append(append(append([]string{}, s.Next...), s.Then...), s.Else...)
	for _, c := range s.Cases {
		out = append(out, c.Next...)
	}
	return out
}

// Pick is where a switch goes for its value (no case: Else).
func (s Step) Pick(v Vars) (string, Targets) {
	got := strings.TrimSpace(unquote(Render(s.Value, v)))
	for _, c := range s.Cases {
		if strings.EqualFold(got, strings.TrimSpace(unquote(Render(c.When, v)))) {
			return got, c.Next
		}
	}
	return got, s.Else
}

// Reaches: step to may come after step from (from itself aside, unless a
// loop leads back to it), not through to itself.
func (d Def) Reaches(from, to string) bool {
	seen := map[string]bool{to: true}
	todo := []string{from}
	for len(todo) > 0 {
		id := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		s, ok := d.Step(id)
		if !ok {
			continue
		}
		for _, x := range s.Outs() {
			if x == to {
				return true
			}
			if !seen[x] {
				seen[x] = true
				todo = append(todo, x)
			}
		}
	}
	return false
}

// CoordinateDef is the workflow a coordinate step runs: this one's roles
// it hands work to (with their groups and vote), its limits, brief and
// gates, the step's instructions; its outputs may be given, none required.
func (d Def) CoordinateDef(s Step, body string) Def {
	c := d
	c.Steps, c.Start, c.Body = nil, nil, body
	keep := func(k string) bool {
		return k != s.Role && (len(s.Roles) == 0 || slices.Contains(s.Roles, k))
	}
	c.Roles = nil
	for _, r := range d.Roles {
		if keep(r.Key) || d.Supervise != nil && r.Key == d.Supervise.Role { // the supervisor watches the step
			c.Roles = append(c.Roles, r)
		}
	}
	c.Parallel = nil
	for _, g := range d.Parallel {
		if g = slices.DeleteFunc(slices.Clone(g), func(k string) bool { return !keep(k) }); len(g) > 1 {
			c.Parallel = append(c.Parallel, g)
		}
	}
	if d.Vote != nil && !slices.ContainsFunc(d.Vote.Roles, func(k string) bool { return !keep(k) }) {
		v := *d.Vote
		c.Vote = &v
	} else {
		c.Vote = nil
	}
	c.Outputs = nil
	for _, f := range d.Outputs {
		f.Required = false
		c.Outputs = append(c.Outputs, f)
	}
	return c
}

var stepIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// skillRe is a skill's name as "/name" calls it (plugin ones "<plugin>:<name>").
var skillRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,99}$`)

// ValidStepID: a step's id; "_" too, as people and agents write it
// ({{steps.goc_nhin_a.output}} reads it), unlike a workflow's key.
func ValidStepID(id string) bool { return stepIDRe.MatchString(id) }

// Starts are the first steps (start, or the first of the list).
func (d Def) Starts() []string {
	if len(d.Start) > 0 {
		return d.Start
	}
	if len(d.Steps) > 0 {
		return []string{d.Steps[0].ID}
	}
	return nil
}

// Position is a step's place on the canvas.
type Position struct {
	X float64 `yaml:"x" json:"x"`
	Y float64 `yaml:"y" json:"y"`
}

// MaxWait bounds a wait step.
const MaxWait = 24 * time.Hour

// WaitFor reads a wait step's For: a Go duration, more than 0 and at most MaxWait.
func WaitFor(v string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil || d <= 0 || d > MaxWait {
		return 0, fmt.Errorf("for %q: thời lượng như 30m, 1h, từ 1s tới 24h", v)
	}
	return d, nil
}

// DefaultMaxLoops bounds a condition's way back.
const DefaultMaxLoops = 5

// MaxStepsRun bounds the steps one run takes, loops included.
const MaxStepsRun = 200

// StepMode: the workflow is a graph of steps (no coordinator).
func (d Def) StepMode() bool { return len(d.Steps) > 0 }

// Step finds a step by id.
func (d Def) Step(id string) (Step, bool) {
	i := slices.IndexFunc(d.Steps, func(s Step) bool { return s.ID == id })
	if i < 0 {
		return Step{}, false
	}
	return d.Steps[i], true
}

var langs = []string{"bash", "node", "python"}
var methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// validateSteps adds the problems of a graph of steps.
func (d Def) validateSteps(add func(string, ...any)) {
	ids := map[string]bool{}
	for _, s := range d.Steps {
		if !ValidStepID(s.ID) || ids[s.ID] {
			add("bước %q: id chỉ dùng chữ thường, số, gạch ngang, gạch dưới và không được trùng", s.ID)
		}
		ids[s.ID] = true
	}
	ref := func(s Step, field string, to Targets, need bool) {
		if len(to) == 0 && need {
			add("bước %q: thiếu %s", s.ID, field)
		}
		for _, x := range to {
			if !ids[x] {
				add("bước %q: %s %q không có", s.ID, field, x)
			}
		}
	}
	for _, x := range d.Start {
		if !ids[x] {
			add("start: bước %q không có", x)
		}
	}
	ends := 0
	for _, s := range d.Steps {
		if !slices.Contains(StepTypes, s.Type) {
			add("bước %q: type phải là %s", s.ID, strings.Join(StepTypes, " | "))
			continue
		}
		if s.MaxLoops < 0 || s.MaxLoops > 50 {
			add("bước %q: max_loops 0–50", s.ID)
		}
		if s.OnError != "" && s.OnError != "stop" && s.OnError != "continue" {
			add("bước %q: on_error là stop | continue", s.ID)
		}
		switch s.Type {
		case StepAgent:
			if _, ok := d.Role(s.Role); !ok {
				add("bước %q: role %q không có trong roles", s.ID, s.Role)
			}
			if strings.TrimSpace(s.Prompt) == "" {
				add("bước %q: thiếu prompt", s.ID)
			}
			if s.Skill != "" && !skillRe.MatchString(s.Skill) {
				add("bước %q: tên skill %q không hợp lệ", s.ID, s.Skill)
			}
		case StepCoordinate:
			if s.Role != "" {
				if _, ok := d.Role(s.Role); !ok {
					add("bước %q: role %q không có trong roles", s.ID, s.Role)
				}
			}
			for _, r := range s.Roles {
				if _, ok := d.Role(r); !ok || r == s.Role {
					add("bước %q: vai %q không có trong roles hoặc là vai điều phối", s.ID, r)
				}
			}
			if len(d.Roles) == 0 || len(d.Roles) == 1 && s.Role != "" {
				add("bước %q: cần ít nhất một vai để giao việc", s.ID)
			}
			if strings.TrimSpace(s.Prompt) == "" {
				add("bước %q: thiếu prompt (hướng dẫn cho agent điều phối)", s.ID)
			}
		case StepWorkflow:
			if !ValidKey(s.Workflow) {
				add("bước %q: thiếu workflow (key quy trình)", s.ID)
			}
		case StepCode:
			if !slices.Contains(langs, s.Lang) {
				add("bước %q: lang là bash | node | python", s.ID)
			}
			if strings.TrimSpace(s.Script) == "" {
				add("bước %q: thiếu script", s.ID)
			}
		case StepHTTP:
			if !strings.HasPrefix(s.URL, "http://") && !strings.HasPrefix(s.URL, "https://") && !strings.HasPrefix(s.URL, "{{") {
				add("bước %q: url phải bắt đầu bằng http:// hoặc https://", s.ID)
			}
			if s.Method != "" && !slices.Contains(methods, strings.ToUpper(s.Method)) {
				add("bước %q: method là %s", s.ID, strings.Join(methods, " | "))
			}
		case StepCondition:
			if strings.TrimSpace(s.If) == "" {
				add("bước %q: thiếu if", s.ID)
			}
			ref(s, "then", s.Then, true)
			ref(s, "else", s.Else, true)
		case StepSwitch:
			if strings.TrimSpace(s.Value) == "" {
				add("bước %q: thiếu value (giá trị để chọn nhánh)", s.ID)
			}
			if len(s.Cases) == 0 {
				add("bước %q: cần ít nhất một case", s.ID)
			}
			seen := map[string]bool{}
			for _, c := range s.Cases {
				w := strings.ToLower(strings.TrimSpace(c.When))
				if w == "" || seen[w] {
					add("bước %q: case %q trống hoặc bị trùng", s.ID, c.When)
				}
				seen[w] = true
				ref(s, "next của case "+strconv.Quote(c.When), c.Next, true)
			}
			ref(s, "else", s.Else, false)
		case StepWait:
			if _, err := WaitFor(s.For); err != nil && !strings.Contains(s.For, "{{") {
				add("bước %q: %s", s.ID, err)
			}
		case StepApprove:
			if strings.TrimSpace(s.Note) == "" {
				add("bước %q: thiếu note (điều người dùng duyệt)", s.ID)
			}
			ref(s, "else", s.Else, false)
		case StepCheck:
			if strings.TrimSpace(s.Command) == "" {
				add("bước %q: thiếu command", s.ID)
			}
			ref(s, "else", s.Else, false)
		case StepEnd:
			ends++
		}
		if s.Type != StepEnd && s.Type != StepCondition && s.Type != StepSwitch {
			ref(s, "next", s.Next, true)
		}
	}
	if ends == 0 {
		add("cần ít nhất một bước end")
	}
}

// StepResult is what a step left for the steps after it.
type StepResult struct {
	Output string `json:"output"`
	Status string `json:"status,omitempty"` // http: the status code; check/approve: passed | failed
	JSON   any    `json:"json,omitempty"`   // the output parsed, when it is JSON
}

// Vars are the data templates read.
type Vars struct {
	Input map[string]string     `json:"input"`
	Steps map[string]StepResult `json:"steps"`
}

// NewResult is out as a step's result (its JSON too, when it is JSON).
func NewResult(out, status string) StepResult {
	r := StepResult{Output: out, Status: status}
	var v any
	if t := strings.TrimSpace(out); (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && json.Unmarshal([]byte(t), &v) == nil {
		r.JSON = v
	}
	return r
}

var tplRe = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.\-]+)\s*\}\}`)

// Render fills a template's {{…}} from vars ("" for what is not there).
func Render(tpl string, v Vars) string {
	return tplRe.ReplaceAllStringFunc(tpl, func(m string) string {
		return lookup(tplRe.FindStringSubmatch(m)[1], v)
	})
}

func lookup(path string, v Vars) string {
	parts := strings.Split(path, ".")
	switch {
	case parts[0] == "input" && len(parts) == 2:
		return v.Input[parts[1]]
	case parts[0] == "steps" && len(parts) >= 3:
		r, ok := v.Steps[parts[1]]
		if !ok {
			return ""
		}
		switch parts[2] {
		case "output":
			return r.Output
		case "status":
			return r.Status
		case "json":
			var cur any = r.JSON
			for _, p := range parts[3:] {
				switch x := cur.(type) {
				case map[string]any:
					cur = x[p]
				case []any:
					i, err := strconv.Atoi(p)
					if err != nil || i < 0 || i >= len(x) {
						return ""
					}
					cur = x[i]
				default:
					return ""
				}
			}
			switch x := cur.(type) {
			case nil:
				return ""
			case string:
				return x
			default:
				b, _ := json.Marshal(x)
				return string(b)
			}
		}
	}
	return ""
}

var condOps = []string{" contains ", " == ", " != ", " >= ", " <= ", " > ", " < "}

// Eval reads a condition: "A op B" (op: == != > < >= <= contains; numbers
// compare as numbers) or one value, true unless empty, false, 0 or null.
// Both sides are templates.
func Eval(expr string, v Vars) bool {
	for _, op := range condOps {
		l, r, ok := strings.Cut(expr, op)
		if !ok {
			continue
		}
		a, b := unquote(Render(l, v)), unquote(Render(r, v))
		fa, ea := strconv.ParseFloat(a, 64)
		fb, eb := strconv.ParseFloat(b, 64)
		num := ea == nil && eb == nil
		switch strings.TrimSpace(op) {
		case "contains":
			return strings.Contains(a, b)
		case "==":
			return a == b || num && fa == fb
		case "!=":
			return !(a == b || num && fa == fb)
		case ">":
			return num && fa > fb || !num && a > b
		case "<":
			return num && fa < fb || !num && a < b
		case ">=":
			return num && fa >= fb || !num && a >= b
		case "<=":
			return num && fa <= fb || !num && a <= b
		}
	}
	s := strings.ToLower(unquote(Render(expr, v)))
	return s != "" && s != "false" && s != "0" && s != "null"
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// MaxHTTPBody is how much of a response an http step keeps.
const MaxHTTPBody = 256 << 10

// DoHTTP runs an http step (already rendered): the status code and the body.
func DoHTTP(ctx context.Context, s Step) (int, string, error) {
	method := strings.ToUpper(s.Method)
	if method == "" {
		method = http.MethodGet
	}
	timeout := time.Duration(s.TimeoutS) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, min(timeout, 2*time.Minute))
	defer cancel()
	var body io.Reader
	if s.Body != "" {
		body = strings.NewReader(s.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.URL, body)
	if err != nil {
		return 0, "", err
	}
	for k, v := range s.Headers {
		req.Header.Set(k, v)
	}
	if s.Body != "" && req.Header.Get("Content-Type") == "" && json.Valid([]byte(s.Body)) {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxHTTPBody))
	if err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, string(raw), nil
}

// ErrStepFailed: a step did not do what it should (its output says why).
var ErrStepFailed = errors.New("bước không thành công")

// StepLabel names a step for the log and the run's chat.
func StepLabel(s Step) string {
	if s.Name != "" {
		return s.Name
	}
	return fmt.Sprintf("%s (%s)", s.ID, s.Type)
}
