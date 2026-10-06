package chat

import "testing"

// A person chatting (web, Discord, Telegram) is there to stop the agents: no
// small cap on how they hand off and report back. What runs unattended
// (automations, monitors) keeps it; a loop still stops at a safety cap.
func TestLimitsFor(t *testing.T) {
	for _, who := range []string{"human:a@x.io", "discord:an", "telegram:binh"} {
		if hops, answers := limitsFor(who); hops < 10 || answers < 20 {
			t.Errorf("%s: %d hops, %d answers", who, hops, answers)
		}
	}
	for _, who := range []string{"automation:Review PR", "monitor:api", ""} {
		if hops, answers := limitsFor(who); hops != 2 || answers != 4 {
			t.Errorf("%s: %d hops, %d answers", who, hops, answers)
		}
	}
}
