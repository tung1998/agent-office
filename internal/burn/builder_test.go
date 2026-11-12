package burn

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Features, read for the project page's progress block (ADR-139): none
// before features.json exists, either JSON shape once it does.
func TestFeaturesForDisplay(t *testing.T) {
	t.Parallel()
	if got := Features(""); got != nil {
		t.Fatalf("no dir: %v", got)
	}
	dir := t.TempDir()
	if got := Features(dir); got != nil {
		t.Fatalf("no features.json yet: %v", got)
	}
	os.WriteFile(filepath.Join(dir, featuresFile), []byte(`[{"id":"f1","title":"Đăng nhập","passes":true},{"id":"f2","title":"Thanh toán","passes":false}]`), 0o644)
	got := Features(dir)
	if len(got) != 2 || got[0] != (Feature{"f1", "Đăng nhập", true}) || got[1] != (Feature{"f2", "Thanh toán", false}) {
		t.Fatalf("array form: %+v", got)
	}
	os.WriteFile(filepath.Join(dir, featuresFile), []byte(`{"features":[{"id":"f1","title":"Đăng nhập","passes":true}]}`), 0o644)
	if got := Features(dir); len(got) != 1 || got[0] != (Feature{"f1", "Đăng nhập", true}) {
		t.Fatalf("wrapped form: %+v", got)
	}
}

// A malformed features.json (e.g. a trailing comma from a bad edit) must not
// be mistaken for "no features": that would silently skip the re-check of
// every previously-passing feature, the builder Burn's whole safety point
// (ADR-139). A passing feature whose acceptance now fails must still send
// the piece back, not merge it as if nothing broke.
func TestCheckedFailsOnMalformedFeaturesJSON(t *testing.T) {
	t.Parallel()
	s, b, repo := mergeTest(t)
	b.Template = "builder"
	ctx := context.Background()
	it := piece(t, s, b, repo, "one", func(x string) string { return x + "nine\n" })
	// trailing comma: invalid JSON in both the bare-list and wrapped shapes
	os.WriteFile(filepath.Join(it.Worktree, featuresFile),
		[]byte(`[{"id":"f1","title":"Đăng nhập","passes":true,"acceptance":"false"},]`), 0o644)
	it.Summary = "xong việc"
	it.Status = "done"

	if ok := s.checked(ctx, b, &it); ok {
		t.Fatalf("checked = true, want false (features.json did not parse, the regression check never ran)")
	}
}
