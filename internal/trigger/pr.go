package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tung1998/agent-office/internal/gitops"
)

// PR is a pull request event, as a PR review automation's payload keeps it
// (compact: GitHub's and Bitbucket's own payloads are large).
type PR struct {
	Provider string `json:"provider"` // github | bitbucket
	Event    string `json:"event"`
	Number   string `json:"number"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	URL      string `json:"url"`
	Source   string `json:"source"` // the branch it merges
	Target   string `json:"target"` // into this one
	Head     string `json:"head"`   // its latest commit
}

// ParsePR reads a GitHub or Bitbucket webhook: a PR opened or with new
// commits is one to review; anything else says why not.
func ParsePR(h http.Header, body []byte) (PR, bool, string) {
	switch {
	case h.Get("X-GitHub-Event") != "":
		if ev := h.Get("X-GitHub-Event"); ev != "pull_request" {
			return PR{}, false, "không phải sự kiện pull request (" + ev + ")"
		}
		var x struct {
			Action string `json:"action"`
			PR     struct {
				Number  int    `json:"number"`
				Title   string `json:"title"`
				HTMLURL string `json:"html_url"`
				Draft   bool   `json:"draft"`
				User    struct {
					Login string `json:"login"`
				} `json:"user"`
				Head struct {
					Ref string `json:"ref"`
					SHA string `json:"sha"`
				} `json:"head"`
				Base struct {
					Ref string `json:"ref"`
				} `json:"base"`
			} `json:"pull_request"`
		}
		if json.Unmarshal(body, &x) != nil {
			return PR{}, false, "payload không đọc được"
		}
		switch x.Action {
		case "opened", "reopened", "synchronize", "ready_for_review":
		default:
			return PR{}, false, "PR " + x.Action + ": không cần review"
		}
		if x.PR.Draft {
			return PR{}, false, "PR nháp: chưa review"
		}
		return PR{Provider: "github", Event: x.Action, Number: fmt.Sprint(x.PR.Number), Title: x.PR.Title, Author: x.PR.User.Login, URL: x.PR.HTMLURL,
			Source: x.PR.Head.Ref, Target: x.PR.Base.Ref, Head: x.PR.Head.SHA}, true, ""
	case h.Get("X-Event-Key") != "":
		ev := h.Get("X-Event-Key")
		if ev != "pullrequest:created" && ev != "pullrequest:updated" {
			return PR{}, false, ev + ": không cần review"
		}
		var x struct {
			PR struct {
				ID     int    `json:"id"`
				Title  string `json:"title"`
				Author struct {
					DisplayName string `json:"display_name"`
				} `json:"author"`
				Links struct {
					HTML struct {
						Href string `json:"href"`
					} `json:"html"`
				} `json:"links"`
				Source struct {
					Branch struct {
						Name string `json:"name"`
					} `json:"branch"`
					Commit struct {
						Hash string `json:"hash"`
					} `json:"commit"`
				} `json:"source"`
				Destination struct {
					Branch struct {
						Name string `json:"name"`
					} `json:"branch"`
				} `json:"destination"`
			} `json:"pullrequest"`
		}
		if json.Unmarshal(body, &x) != nil {
			return PR{}, false, "payload không đọc được"
		}
		return PR{Provider: "bitbucket", Event: ev, Number: fmt.Sprint(x.PR.ID), Title: x.PR.Title, Author: x.PR.Author.DisplayName, URL: x.PR.Links.HTML.Href,
			Source: x.PR.Source.Branch.Name, Target: x.PR.Destination.Branch.Name, Head: x.PR.Source.Commit.Hash}, true, ""
	}
	return PR{}, false, "không phải webhook của GitHub/Bitbucket"
}

// maxDiff is the most of a PR's diff an agent is given.
const maxDiff = 150 << 10

// prDiff fetches the PR's two branches and returns their diff (with a
// summary on top), or why it could not.
func prDiff(ctx context.Context, root string, pr PR) string {
	heads := []string{pr.Source}
	if pr.Provider == "github" && pr.Number != "" { // a PR from a fork: its branch is not on origin
		heads = []string{"refs/pull/" + pr.Number + "/head", pr.Source}
	}
	diff, err := gitops.BranchDiff(ctx, root, pr.Target, heads, maxDiff)
	if err != nil {
		return "(Không lấy được diff: " + err.Error() + ". Đọc thay đổi bằng git diff / git show nếu được.)"
	}
	if strings.TrimSpace(diff) == "" {
		return "(Hai nhánh không khác nhau.)"
	}
	return "```diff\n" + diff + "\n```"
}
