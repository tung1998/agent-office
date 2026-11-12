package burn

import (
	"errors"
	"testing"
)

// A CLI that gave up on a passing API failure leaves the piece untried and
// the Burn waiting, like any service down (ADR-152, ADR-260): the daily
// upgrade's runs died on "claude: exit status 1 · lỗi API cuối: 500 …".
func TestTransientIsTechnical(t *testing.T) {
	t.Parallel()
	for _, msg := range []string{
		"claude: exit status 1 · lỗi API cuối: 500 api_error, lần 10/10 · sau 3m",
		"claude: exit status 1 · lỗi API cuối: 529 overloaded_error, lần 10/10",
		"codex: stream disconnected before completion",
	} {
		if !technical(errors.New(msg)) || !technicalText(msg) {
			t.Errorf("not technical: %s", msg)
		}
	}
	if technical(errors.New("go test ./... failed: 3 tests")) || technicalText("claude: exit status 1") {
		t.Error("a piece's own failure read as technical")
	}
}
