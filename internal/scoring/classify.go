// Package scoring scores finished jobs and ranks model/effort configurations
// for suggestions (ADR-267). It only suggests: nothing here changes a chat or
// an agent.
package scoring

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Task kinds and difficulties a job is labelled with.
const (
	KindAsk        = "ask"
	KindBugfix     = "bugfix"
	KindFeature    = "feature"
	KindReview     = "review"
	KindBurn       = "burn"
	KindAutomation = "automation"
	KindWorkflow   = "workflow"

	Light  = "light"
	Medium = "medium"
	Heavy  = "heavy"
)

// Kinds and Difficulties are the valid labels.
var (
	Kinds        = []string{KindAsk, KindBugfix, KindFeature, KindReview, KindBurn, KindAutomation, KindWorkflow}
	Difficulties = []string{Light, Medium, Heavy}
)

// Source is where the work came from, besides its text.
type Source struct {
	Purpose     string // the chat's purpose ("automation" …)
	Burn        bool
	Workflow    bool
	Attachments int
	Files       int // files the request names or attaches
}

var (
	reviewWords  = []string{"review", "soát", "code review"}
	bugWords     = []string{"lỗi", "bug", "fix", "sửa", "crash", "hỏng", "không chạy"}
	featureWords = []string{"thêm", "tạo", "tính năng", "feature", "implement", "triển khai", "refactor", "xây dựng", "build"}
	heavyWords   = []string{"refactor", "kiến trúc", "architecture", "migration", "toàn bộ hệ thống", "rewrite", "viết lại"}
)

func hasAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// Classify labels a request with its kind and difficulty by plain rules (no
// model call): the same input always gets the same labels.
func Classify(text string, src Source) (kind, difficulty string) {
	t := strings.ToLower(text)
	switch {
	case src.Purpose == "automation":
		kind = KindAutomation
	case src.Workflow:
		kind = KindWorkflow
	case src.Burn:
		kind = KindBurn
	case hasAny(t, reviewWords):
		kind = KindReview
	case hasAny(t, bugWords):
		kind = KindBugfix
	case hasAny(t, featureWords):
		kind = KindFeature
	default:
		kind = KindAsk
	}
	n := utf8.RuneCountInString(text)
	files := src.Files + src.Attachments
	switch {
	case n > 1200 || files > 5 || hasAny(t, heavyWords):
		difficulty = Heavy
	case n < 200 && files == 0 && kind == KindAsk:
		difficulty = Light
	default:
		difficulty = Medium
	}
	return kind, difficulty
}

var (
	correctPhrases = []string{"chưa đúng", "không đúng", "không phải", "sửa lại", "làm lại", "hoàn tác", "nhầm rồi",
		"not what", "that's wrong", "thats wrong", "incorrect", "try again", "undo", "revert"}
	correctWords = map[string]bool{"sai": true, "wrong": true}
)

// Corrective tells whether the person's next message reads as correcting
// the agent's answer before it.
func Corrective(text string) bool {
	t := strings.ToLower(text)
	if hasAny(t, correctPhrases) {
		return true
	}
	for _, w := range strings.FieldsFunc(t, func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' }) {
		if correctWords[w] {
			return true
		}
	}
	return false
}
