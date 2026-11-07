// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package resolver

import (
	"net/netip"
	"testing"
	"time"
)

func TestExitNodeResolversUsesSetServers(t *testing.T) {
	r := newResolver(t)
	defer r.Close()
	r.SetExitNodeDNSServers([]netip.AddrPort{
		netip.MustParseAddrPort("1.1.1.1:53"),
		netip.MustParseAddrPort("8.8.8.8:53"),
	})
	got, err := r.exitNodeResolvers()
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		addr  string
		delay time.Duration
	}{{"1.1.1.1:53", 0}, {"8.8.8.8:53", 200 * time.Millisecond}}
	if len(got) != len(want) {
		t.Fatalf("got %d resolvers, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].name.Addr != w.addr || got[i].startDelay != w.delay {
			t.Errorf("resolver %d = %s after %v, want %s after %v", i, got[i].name.Addr, got[i].startDelay, w.addr, w.delay)
		}
	}
}
