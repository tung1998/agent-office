package api

import "time"

// SetGitWait shortens how long a fetch or push request waits for git (tests
// of the background path); the returned func puts it back.
func SetGitWait(d time.Duration) func() {
	old := gitWait
	gitWait = d
	return func() { gitWait = old }
}

// SetComposioBase points office's Composio client at a fake (ADR-282).
func SetComposioBase(u string) func() {
	old := composioBase
	composioBase = u
	return func() { composioBase = old }
}
