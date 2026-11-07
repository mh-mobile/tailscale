// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package resolver

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	dns "golang.org/x/net/dns/dnsmessage"
)

// TestExitDNSForwardedIDs needs no outside DNS: concurrent exit-node queries
// that all carry ID 0 (as DoH clients send them) must reach the upstream
// with distinct IDs, and each reply must carry the client's ID 0 again.
func TestExitDNSForwardedIDs(t *testing.T) {
	up, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	var mu sync.Mutex
	seen := map[uint16]int{}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := up.ReadFrom(buf)
			if err != nil {
				return
			}
			var p dns.Parser
			h, err := p.Start(buf[:n])
			if err != nil {
				continue
			}
			q, _ := p.Question()
			mu.Lock()
			seen[h.ID]++
			mu.Unlock()
			b := dns.NewBuilder(nil, dns.Header{ID: h.ID, Response: true, RecursionAvailable: true})
			b.StartQuestions()
			b.Question(q)
			b.StartAnswers()
			b.AResource(dns.ResourceHeader{Name: q.Name, Class: dns.ClassINET, TTL: 60}, dns.AResource{A: [4]byte{203, 0, 113, 1}})
			reply, _ := b.Finish()
			up.WriteTo(reply, addr)
		}
	}()

	r := newResolver(t)
	defer r.Close()
	r.SetExitNodeDNSServers([]netip.AddrPort{netip.MustParseAddrPort(up.LocalAddr().String())})
	const n = 30
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := dns.NewBuilder(nil, dns.Header{ID: 0, RecursionDesired: true})
			b.StartQuestions()
			b.Question(dns.Question{Name: dns.MustNewName("q" + string(rune('a'+i%26)) + ".example."), Type: dns.TypeA, Class: dns.ClassINET})
			q, _ := b.Finish()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			res, err := r.HandlePeerDNSQuery(ctx, q, netip.MustParseAddrPort("100.101.0.1:5353"), func(string) bool { return true })
			if err != nil {
				t.Errorf("query %d: %v", i, err)
				return
			}
			if res[0] != 0 || res[1] != 0 {
				t.Errorf("query %d: reply ID %x%x, want the client's 0000", i, res[0], res[1])
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(seen) < n {
		t.Errorf("upstream saw %d distinct IDs for %d concurrent queries (%v); a relay that matches replies by ID drops the rest", len(seen), n, seen)
	}
}
