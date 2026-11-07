// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package tsnet

import (
	"context"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"tailscale.com/ipn"
	"tailscale.com/ipn/store/mem"
	"tailscale.com/net/tsaddr"
	"tailscale.com/tailcfg"
	"tailscale.com/tstest"
	"tailscale.com/util/must"
)

// TestFallbackUDPThroughExitNode checks that UDP sent through an exit node
// reaches Server.FallbackUDP with the client's source and the original
// destination, and that UDP addressed to the exit node itself never does.
func TestFallbackUDPThroughExitNode(t *testing.T) {
	tstest.ResourceCheck(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	type flow struct{ src, dst netip.AddrPort }
	calls := make(chan flow, 16)

	controlURL, c := startControl(t)
	s1 := &Server{
		Dir:        t.TempDir(),
		ControlURL: controlURL,
		Hostname:   "s1",
		Store:      new(mem.Store),
		Ephemeral:  true,
		FallbackUDP: func(src, dst netip.AddrPort) bool {
			calls <- flow{src, dst}
			return false // drop: don't send test packets to the internet
		},
	}
	t.Cleanup(func() { s1.Close() })
	s1status, err := s1.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s1ip := s1status.TailscaleIPs[0]
	s1PubKey := s1status.Self.PublicKey

	lc1 := must.Get(s1.LocalClient())
	must.Get(lc1.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:              ipn.Prefs{AdvertiseRoutes: tsaddr.ExitRoutes()},
		AdvertiseRoutesSet: true,
	}))
	c.SetSubnetRoutes(s1PubKey, tsaddr.ExitRoutes())

	s2, s2ip, _ := startServer(t, ctx, controlURL, "s2")
	lc2 := must.Get(s2.LocalClient())
	pingCtx, cancelPing := pingTimeout(ctx)
	defer cancelPing()
	must.Get(lc2.Ping(pingCtx, s1ip, tailcfg.PingTSMP))
	must.Get(lc2.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:         ipn.Prefs{ExitNodeID: c.Node(s1PubKey).StableID},
		ExitNodeIDSet: true,
	}))

	dst := netip.MustParseAddrPort("203.0.113.42:9") // TEST-NET-3
	if err := tstest.WaitFor(30*time.Second, func() error {
		p, ok := s2.lb.PeerForIP(dst.Addr())
		if !ok || p.Node.Key() != s1PubKey {
			return fmt.Errorf("exit route via s1 not installed yet")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	send := func(to netip.AddrPort) {
		conn, err := s2.Dial(ctx, "udp", to.String())
		if err != nil {
			t.Fatalf("s2.Dial(udp, %v): %v", to, err)
		}
		defer conn.Close()
		if _, err := conn.Write([]byte("hello")); err != nil {
			t.Fatalf("write to %v: %v", to, err)
		}
	}

	// UDP to s1's own address with no listener must not be offered.
	send(netip.AddrPortFrom(s1ip, 9999))
	send(dst)
	select {
	case f := <-calls:
		if f.dst != dst {
			t.Fatalf("FallbackUDP got dst %v; want %v (self-addressed UDP must not be offered)", f.dst, dst)
		}
		if f.src.Addr() != s2ip {
			t.Errorf("FallbackUDP got src %v; want s2 %v", f.src, s2ip)
		}
	case <-ctx.Done():
		t.Fatal("FallbackUDP was not called for UDP through the exit node")
	}
}
