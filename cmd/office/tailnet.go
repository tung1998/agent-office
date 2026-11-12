package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// Tailscale's address ranges: 100.64.0.0/10 (CGNAT) and fd7a:115c:a1e0::/48.
var tailnetRanges = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")}

// tailnetAddrs: this machine's Tailscale addresses (none when it is down).
func tailnetAddrs() []netip.Addr {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, a := range addrs {
		pfx, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		if ip := pfx.Addr().Unmap(); inTailnet(ip) {
			out = append(out, ip)
		}
	}
	return out
}

func inTailnet(ip netip.Addr) bool {
	for _, r := range tailnetRanges {
		if r.Contains(ip) {
			return true
		}
	}
	return false
}

// uiListenArgs (ADR-272): the dashboard's port answers on this machine and
// on its Tailscale addresses, not on the LAN (a café's Wi-Fi, a coworker on
// the same network), unless the person asks for it (server.ui_lan or
// --ui-lan), as Paseo and Orca do. local (server.ui_local or --ui-local)
// keeps it on this machine only, Tailscale off too; lan wins over local.
func uiListenArgs(port string, lan, local bool) []string {
	if lan {
		return []string{"--ui-listen", ":" + port}
	}
	if local {
		return []string{"--ui-listen", "127.0.0.1:" + port}
	}
	return []string{"--ui-listen", "127.0.0.1:" + port, "--ui-tailnet"}
}

// serveTailnet serves srv on port at each Tailscale address of this machine,
// checking again every interval for one that comes up later (Tailscale after
// office at boot). An address another program holds (a forwarder) is skipped,
// said once.
func serveTailnet(ctx context.Context, srv *http.Server, port string, interval time.Duration, logw io.Writer) {
	bound, warned := map[netip.Addr]bool{}, map[netip.Addr]bool{}
	try := func() {
		for _, a := range tailnetAddrs() {
			if bound[a] {
				continue
			}
			ln, err := net.Listen("tcp", net.JoinHostPort(a.String(), port))
			if err != nil {
				if !warned[a] {
					warned[a] = true
					fmt.Fprintf(logw, "office: chưa nghe được %s:%s qua Tailscale (%v)\n", a, port, err)
				}
				continue
			}
			bound[a] = true
			fmt.Fprintf(logw, "office: dashboard cũng nghe tại %s (Tailscale)\n", net.JoinHostPort(a.String(), port))
			go func() { _ = srv.Serve(ln) }()
		}
	}
	try()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			try()
		}
	}
}
