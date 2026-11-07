// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package resolver

import (
	"context"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dns "golang.org/x/net/dns/dnsmessage"
)

// TestExitDNSConcurrentRepro sends concurrent exit-node DNS queries the way
// a DoH client does (txid 0) to this host's real resolver. Opt-in only.
func TestExitDNSConcurrentRepro(t *testing.T) {
	if os.Getenv("REQCOVE_REPRO") == "" {
		t.Skip("set REQCOVE_REPRO=1; queries this host's real resolver")
	}
	r := newResolver(t)
	defer r.Close()
	// e.g. REQCOVE_EXIT_DNS=1.1.1.1:53 to bypass /etc/resolv.conf.
	if v := os.Getenv("REQCOVE_EXIT_DNS"); v != "" {
		r.SetExitNodeDNSServers([]netip.AddrPort{netip.MustParseAddrPort(v)})
	}
	qtype := dns.TypeA
	if os.Getenv("REQCOVE_QTYPE") == "HTTPS" {
		qtype = dns.Type(65)
	}
	doms := strings.Fields("icloud.com google.com notion.so instagram.com x.com line.me zoom.us openai.com slack.com youtube.com microsoft.com wikipedia.org cloudflare.com amazon.co.jp netflix.com go.dev figma.com facebook.com anthropic.com python.org tailscale.com docker.com mitmproxy.org npmjs.com rust-lang.org nhk.or.jp apple.com github.com example.com yahoo.co.jp")
	var ok atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for _, d := range doms {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := dns.NewBuilder(nil, dns.Header{ID: reproTxID(), RecursionDesired: true})
			b.StartQuestions()
			b.Question(dns.Question{Name: dns.MustNewName(d + "."), Type: qtype, Class: dns.ClassINET})
			q, _ := b.Finish()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			res, err := r.HandlePeerDNSQuery(ctx, q, netip.MustParseAddrPort("100.101.0.1:5353"), func(string) bool { return true })
			if err != nil {
				t.Logf("%s: %v (%v)", d, err, time.Since(start).Round(time.Millisecond))
				return
			}
			if res[0] != q[0] || res[1] != q[1] {
				t.Errorf("%s: reply ID %x%x, want the query's %x%x", d, res[0], res[1], q[0], q[1])
			}
			ok.Add(1)
		}()
	}
	wg.Wait()
	t.Logf("ok %d of %d", ok.Load(), len(doms))
	if int(ok.Load()) != len(doms) {
		t.Errorf("%d of %d concurrent exit-node DNS queries failed", len(doms)-int(ok.Load()), len(doms))
	}
}

func reproTxID() uint16 {
	if os.Getenv("REQCOVE_RANDOM_TXID") != "" {
		return uint16(time.Now().UnixNano())
	}
	return 0
}
