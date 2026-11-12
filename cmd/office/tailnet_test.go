package main

import (
	"net/netip"
	"slices"
	"testing"
)

// ADR-272: the dashboard's port is this machine's and Tailscale's, the LAN
// only when asked.
func TestUIListenArgs(t *testing.T) {
	if got := uiListenArgs("2704", false, false); !slices.Equal(got, []string{"--ui-listen", "127.0.0.1:2704", "--ui-tailnet"}) {
		t.Fatalf("default = %v", got)
	}
	if got := uiListenArgs("2704", true, false); !slices.Equal(got, []string{"--ui-listen", ":2704"}) {
		t.Fatalf("lan = %v", got)
	}
	if got := uiListenArgs("2704", false, true); !slices.Equal(got, []string{"--ui-listen", "127.0.0.1:2704"}) {
		t.Fatalf("local = %v", got)
	}
	if got := uiListenArgs("2704", true, true); !slices.Equal(got, []string{"--ui-listen", ":2704"}) {
		t.Fatalf("lan+local = %v", got)
	}
}

func TestInTailnet(t *testing.T) {
	for ip, want := range map[string]bool{
		"100.115.62.90":          true,
		"100.64.0.1":             true,
		"100.128.0.1":            false, // past 100.64.0.0/10
		"192.168.1.20":           false,
		"10.0.0.5":               false,
		"127.0.0.1":              false,
		"fd7a:115c:a1e0::8f01:1": true,
		"fe80::1":                false,
	} {
		if got := inTailnet(netip.MustParseAddr(ip)); got != want {
			t.Errorf("inTailnet(%s) = %v, want %v", ip, got, want)
		}
	}
}
