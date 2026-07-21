// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package membership

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/log"
)

// High ports avoid clashing with local services. Each alloc takes a unique block.
var nextGossipPort = int64(47800)

func allocGossipPorts(n int) []int {
	base := int(atomic.AddInt64(&nextGossipPort, int64(n))) - n
	ports := make([]int, n)
	for i := 0; i < n; i++ {
		ports[i] = base + i
	}
	return ports
}

type fakeDNS struct {
	mu    sync.Mutex
	hosts []string
	err   error
}

func (f *fakeDNS) set(hosts ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hosts = append([]string(nil), hosts...)
	f.err = nil
}

func (f *fakeDNS) setError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
	f.hosts = nil
}

func (f *fakeDNS) lookup(ctx context.Context, host string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return append([]string(nil), f.hosts...), nil
}

type callbackProbe struct {
	mu          sync.Mutex
	rebalanceN  int
	leaveAddrs  []string
	rebalanceCh chan struct{}
	leaveCh     chan struct{}
}

func newCallbackProbe() *callbackProbe {
	return &callbackProbe{
		rebalanceCh: make(chan struct{}, 64),
		leaveCh:     make(chan struct{}, 64),
	}
}

func (p *callbackProbe) onRebalance() {
	p.mu.Lock()
	p.rebalanceN++
	p.mu.Unlock()
	select {
	case p.rebalanceCh <- struct{}{}:
	default:
	}
}

func (p *callbackProbe) onMemberLeave(addr string) {
	p.mu.Lock()
	p.leaveAddrs = append(p.leaveAddrs, addr)
	p.mu.Unlock()
	select {
	case p.leaveCh <- struct{}{}:
	default:
	}
}

func (p *callbackProbe) rebalances() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rebalanceN
}

func (p *callbackProbe) leaves() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.leaveAddrs...)
}

func (p *callbackProbe) leaveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.leaveAddrs)
}

type testNode struct {
	id     string
	ip     string
	port   int
	grpc   string
	gossip string
	probe  *callbackProbe
	impl   *membershipImpl
	mem    Membership
}

func gossipAddr(port int) string {
	return net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
}

func newStaticNode(t *testing.T, id string, port int, seeds []string, probe *callbackProbe) *testNode {
	t.Helper()
	return newTestNode(t, id, port, config.DiscoveryConfig{
		Mode:            "static",
		StaticAddresses: seeds,
	}, nil, probe)
}

// newDNSNode binds 127.0.0.1:port. Fake DNS must return host:port dial targets.
func newDNSNode(t *testing.T, id string, port int, dns *fakeDNS, probe *callbackProbe) *testNode {
	t.Helper()
	return newTestNode(t, id, port, config.DiscoveryConfig{
		Mode:               "dns",
		DNSAddress:         "fake.service.local",
		DNSPort:            port, // unused when fake returns host:port
		DNSRefreshInterval: 100 * time.Millisecond,
	}, dns.lookup, probe)
}

func newTestNode(
	t *testing.T,
	id string,
	port int,
	discovery config.DiscoveryConfig,
	dnsLookup lookupDNSHosts,
	probe *callbackProbe,
) *testNode {
	t.Helper()
	if probe == nil {
		probe = newCallbackProbe()
	}
	gossip := gossipAddr(port)
	grpc := fmt.Sprintf("127.0.0.1:%d", 10000+port)
	cfg := &config.MembershipConfig{
		BindAddress:           gossip,
		AdvertiseAddress:      gossip,
		NumberOfVNodes:        32,
		MinMembersBeforeReady: 1,
		Discovery:             discovery,
	}
	mem := newMembershipWithLookup(cfg, log.NewDefaultLogger(), id, grpc, probe.onRebalance, probe.onMemberLeave, dnsLookup)
	impl := mem.(*membershipImpl)
	node := &testNode{
		id:     id,
		ip:     "127.0.0.1",
		port:   port,
		grpc:   grpc,
		gossip: gossip,
		probe:  probe,
		impl:   impl,
		mem:    mem,
	}
	t.Cleanup(func() { node.mem.Stop() })
	return node
}

func (n *testNode) start(t *testing.T) {
	t.Helper()
	require.NoError(t, n.mem.Start())
}

func requireSeesEventually(t *testing.T, viewer *testNode, peers ...*testNode) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, peer := range peers {
			if viewer.mem.GetGrpcAddressForMember(peer.id) != peer.grpc {
				return false
			}
		}
		return true
	}, 15*time.Second, 50*time.Millisecond, "%s should see peers", viewer.id)
}

func requireNotSeeEventually(t *testing.T, viewer *testNode, peerID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return viewer.mem.GetGrpcAddressForMember(peerID) == ""
	}, 15*time.Second, 50*time.Millisecond, "%s should not see %s", viewer.id, peerID)
}

func requireRingAgreement(t *testing.T, nodes []*testNode, totalShards int) {
	t.Helper()
	require.Eventually(t, func() bool {
		if len(nodes) == 0 {
			return true
		}
		for i := 0; i < totalShards; i++ {
			key := fmt.Sprintf("%d", i)
			want := nodes[0].mem.GetMemberIDForKey(key)
			for _, node := range nodes[1:] {
				if node.mem.GetMemberIDForKey(key) != want {
					return false
				}
			}
		}
		return true
	}, 15*time.Second, 50*time.Millisecond, "ring disagreement")
}

func requireLeaveContains(t *testing.T, probe *callbackProbe, addr string) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, got := range probe.leaves() {
			if got == addr {
				return true
			}
		}
		return false
	}, 15*time.Second, 50*time.Millisecond, "missing leave for %s", addr)
}
