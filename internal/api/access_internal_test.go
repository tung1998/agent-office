package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every route naming a thing ({id}…) is either about one project, which
// routeProject knows, or about the whole office (ADR-224): a new kind of
// thing must be added to one or the other, or a manager would reach another
// project's.
func TestRoutesClassified(t *testing.T) {
	project := map[string]bool{ // routeProject's cases
		"projects": true, "agents": true, "conversations": true, "chat/turns": true, "patches": true, "automations": true,
		"workflows": true, "workflow-runs": true, "jobs": true, "processes": true, "monitors": true, "actions": true,
		"channels": true, "memories": true, "memory-revisions": true, "collections": true, "revisions": true,
		"attachments": true, "burn-items": true, "burn-review-profiles": true,
	}
	office := map[string]bool{ // the whole office's: admin only, or the person's own
		"agent-library": true, "automation/library": true, "automation/templates": true, "cli-jobs": true, "cli-tools": true,
		"mcp/servers": true, "me/prefs": true, "me/tokens": true, "me/passkeys": true, "peers": true, "peers/outgoing": true, "peers/requests": true,
		"providers": true, "system/processes": true, "users": true, "workflow-library": true,
		"peer/chats": true, "peer/projects": true, // another office's calls: its own auth (peerCall)
	}
	files, _ := filepath.Glob("*.go")
	route := regexp.MustCompile(`"(?:GET|POST|PUT|PATCH|DELETE) /api/([^"{]+)/\{`)
	seen := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range route.FindAllStringSubmatch(string(src), -1) {
			seen++
			if head := m[1]; !project[head] && !office[head] {
				t.Errorf("%s: route /api/%s/{…} is neither a project's (routeProject) nor the office's", f, head)
			}
		}
	}
	if seen < 100 {
		t.Fatalf("only %d routes found: the pattern no longer matches", seen)
	}
}
