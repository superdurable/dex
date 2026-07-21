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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/log"
)

func TestMembership_DNS_ShrinkDoesNotEvict(t *testing.T) {
	ports := allocGossipPorts(3)
	dns := &fakeDNS{}
	probeA := newCallbackProbe()

	a := newDNSNode(t, "shrink-a", ports[0], dns, probeA)
	b := newDNSNode(t, "shrink-b", ports[1], dns, newCallbackProbe())
	c := newDNSNode(t, "shrink-c", ports[2], dns, newCallbackProbe())
	a.start(t)
	b.start(t)
	c.start(t)
	dns.set(gossipAddr(ports[0]), gossipAddr(ports[1]), gossipAddr(ports[2]))
	requireSeesEventually(t, a, b, c)

	leavesBefore := probeA.leaveCount()
	dns.set(gossipAddr(ports[0]), gossipAddr(ports[1]))
	require.Never(t, func() bool {
		return probeA.leaveCount() > leavesBefore || a.mem.GetGrpcAddressForMember(c.id) == ""
	}, 400*time.Millisecond, 50*time.Millisecond)
	requireSeesEventually(t, a, b, c)
}

func TestMembership_DNS_UnreachableThenJoin(t *testing.T) {
	ports := allocGossipPorts(3)
	dns := &fakeDNS{}
	probeA := newCallbackProbe()

	a := newDNSNode(t, "unreach-a", ports[0], dns, probeA)
	b := newDNSNode(t, "unreach-b", ports[1], dns, newCallbackProbe())
	a.start(t)
	b.start(t)
	dns.set(gossipAddr(ports[0]), gossipAddr(ports[1]))
	requireSeesEventually(t, a, b)

	dns.set(gossipAddr(ports[0]), gossipAddr(ports[1]), gossipAddr(ports[2]))
	require.Never(t, func() bool {
		return a.mem.GetGrpcAddressForMember("unreach-c") != ""
	}, 400*time.Millisecond, 50*time.Millisecond)
	requireSeesEventually(t, a, b)

	c := newDNSNode(t, "unreach-c", ports[2], dns, newCallbackProbe())
	c.start(t)
	requireSeesEventually(t, a, b, c)
}

func TestMembership_Static_BootstrapUnreachableSeedFails(t *testing.T) {
	ports := allocGossipPorts(2)
	deadSeed := gossipAddr(ports[1])
	probe := newCallbackProbe()
	node := newStaticNode(t, "boot-fail", ports[0], []string{deadSeed}, probe)
	err := node.mem.Start()
	require.Error(t, err)
}

func TestMembership_DNS_BootstrapEmptyOK(t *testing.T) {
	ports := allocGossipPorts(2)
	dns := &fakeDNS{}
	dns.set()
	a := newDNSNode(t, "boot-empty-a", ports[0], dns, newCallbackProbe())
	a.start(t)

	dns.set(gossipAddr(ports[1]))
	b := newDNSNode(t, "boot-empty-b", ports[1], dns, newCallbackProbe())
	b.start(t)
	requireSeesEventually(t, a, b)
}

func TestMembership_DNS_SelfAndDupFilter(t *testing.T) {
	ports := allocGossipPorts(2)
	dns := &fakeDNS{}
	probeA := newCallbackProbe()

	a := newDNSNode(t, "dup-a", ports[0], dns, probeA)
	a.start(t)
	rebBefore := probeA.rebalances()

	dns.set(gossipAddr(ports[0]), gossipAddr(ports[0]), gossipAddr(ports[1]), gossipAddr(ports[1]))
	b := newDNSNode(t, "dup-b", ports[1], dns, newCallbackProbe())
	b.start(t)
	requireSeesEventually(t, a, b)

	require.Less(t, probeA.rebalances()-rebBefore, 20)
}

func TestMembership_SameNameReclaim(t *testing.T) {
	ports := allocGossipPorts(3)
	probeA := newCallbackProbe()

	a := newStaticNode(t, "reclaim-a", ports[0], nil, probeA)
	a.start(t)
	b1 := newStaticNode(t, "reclaim-b", ports[1], []string{a.gossip}, newCallbackProbe())
	b1.start(t)
	requireSeesEventually(t, a, b1)
	oldGrpc := b1.grpc

	b1.mem.Stop()
	requireLeaveContains(t, probeA, oldGrpc)
	requireNotSeeEventually(t, a, "reclaim-b")

	b2 := newStaticNode(t, "reclaim-b", ports[2], []string{a.gossip}, newCallbackProbe())
	b2.start(t)
	requireSeesEventually(t, a, b2)
	require.Equal(t, b2.grpc, a.mem.GetGrpcAddressForMember("reclaim-b"))
	require.NotEqual(t, oldGrpc, b2.grpc)
	requireRingAgreement(t, []*testNode{a, b2}, 32)
}

func TestMembership_CrossNodeRingAgreement(t *testing.T) {
	ports := allocGossipPorts(3)
	a := newStaticNode(t, "ring-a", ports[0], nil, newCallbackProbe())
	a.start(t)
	b := newStaticNode(t, "ring-b", ports[1], []string{a.gossip}, newCallbackProbe())
	c := newStaticNode(t, "ring-c", ports[2], []string{a.gossip}, newCallbackProbe())
	b.start(t)
	c.start(t)
	requireSeesEventually(t, a, b, c)
	requireRingAgreement(t, []*testNode{a, b, c}, 64)

	b.mem.Stop()
	requireNotSeeEventually(t, a, b.id)
	requireRingAgreement(t, []*testNode{a, c}, 64)
}

func TestMembership_MinMembersBeforeReadyInterruptedByStop(t *testing.T) {
	ports := allocGossipPorts(1)
	probe := newCallbackProbe()
	gossip := gossipAddr(ports[0])
	cfg := &config.MembershipConfig{
		BindAddress:           gossip,
		AdvertiseAddress:      gossip,
		NumberOfVNodes:        32,
		MinMembersBeforeReady: 3,
		Discovery: config.DiscoveryConfig{
			Mode: "static",
		},
	}
	mem := newMembershipWithLookup(
		cfg, log.NewDefaultLogger(), "min-a", "127.0.0.1:59999",
		probe.onRebalance, probe.onMemberLeave, nil,
	)
	impl := mem.(*membershipImpl)
	t.Cleanup(func() { mem.Stop() })

	done := make(chan error, 1)
	go func() { done <- mem.Start() }()

	require.Eventually(t, func() bool { return impl.mlist != nil }, 5*time.Second, 20*time.Millisecond)
	mem.Stop()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return after Stop")
	}
	mem.Stop()
}

func TestMembership_DNS_LookupErrorsThenRecover(t *testing.T) {
	ports := allocGossipPorts(2)
	dns := &fakeDNS{}
	dns.setError(errors.New("dns down"))
	probeA := newCallbackProbe()

	a := newDNSNode(t, "err-a", ports[0], dns, probeA)
	dns.set()
	a.start(t)
	leavesBefore := probeA.leaveCount()

	dns.setError(errors.New("dns down"))
	require.Never(t, func() bool {
		return probeA.leaveCount() > leavesBefore
	}, 400*time.Millisecond, 50*time.Millisecond)

	dns.set(gossipAddr(ports[1]))
	b := newDNSNode(t, "err-b", ports[1], dns, newCallbackProbe())
	b.start(t)
	requireSeesEventually(t, a, b)
}

func TestMembership_DNS_StaggeredStart(t *testing.T) {
	ports := allocGossipPorts(2)
	dns := &fakeDNS{}
	dns.set()

	a := newDNSNode(t, "stag-a", ports[0], dns, newCallbackProbe())
	a.start(t)
	b := newDNSNode(t, "stag-b", ports[1], dns, newCallbackProbe())
	b.start(t)
	require.Never(t, func() bool {
		return a.mem.GetGrpcAddressForMember(b.id) != ""
	}, 300*time.Millisecond, 50*time.Millisecond)

	dns.set(gossipAddr(ports[0]), gossipAddr(ports[1]))
	requireSeesEventually(t, a, b)
	requireSeesEventually(t, b, a)
}

func TestMembership_ShardOwnershipMovesOnLeave(t *testing.T) {
	ports := allocGossipPorts(3)
	const totalShards = 32
	probeA := newCallbackProbe()

	a := newStaticNode(t, "shard-a", ports[0], nil, probeA)
	a.start(t)
	b := newStaticNode(t, "shard-b", ports[1], []string{a.gossip}, newCallbackProbe())
	c := newStaticNode(t, "shard-c", ports[2], []string{a.gossip}, newCallbackProbe())
	b.start(t)
	c.start(t)
	requireSeesEventually(t, a, b, c)
	requireRingAgreement(t, []*testNode{a, b, c}, totalShards)

	bShards := b.mem.GetShardsForMember(b.id, totalShards)
	require.NotEmpty(t, bShards)
	rebBefore := probeA.rebalances()

	b.mem.Stop()
	requireLeaveContains(t, probeA, b.grpc)
	requireNotSeeEventually(t, a, b.id)
	require.Greater(t, probeA.rebalances(), rebBefore)

	require.Eventually(t, func() bool {
		for _, shardID := range bShards {
			owner := a.mem.GetMemberIDForKey(fmt.Sprintf("%d", shardID))
			if owner == b.id {
				return false
			}
			if owner != a.id && owner != c.id {
				return false
			}
		}
		return true
	}, 15*time.Second, 50*time.Millisecond)
	requireRingAgreement(t, []*testNode{a, c}, totalShards)
}

func TestMembership_GrpcMetaUpdateWithoutRename(t *testing.T) {
	ports := allocGossipPorts(2)
	probeA := newCallbackProbe()

	a := newStaticNode(t, "meta-a", ports[0], nil, probeA)
	a.start(t)
	b := newStaticNode(t, "meta-b", ports[1], []string{a.gossip}, newCallbackProbe())
	b.start(t)
	requireSeesEventually(t, a, b)

	leavesBefore := probeA.leaveCount()
	rebBefore := probeA.rebalances()
	newGrpc := "127.0.0.1:59998"
	b.impl.grpcAddress = newGrpc
	require.NoError(t, b.impl.mlist.UpdateNode(2*time.Second))

	require.Eventually(t, func() bool {
		return a.mem.GetGrpcAddressForMember(b.id) == newGrpc
	}, 15*time.Second, 50*time.Millisecond)
	require.Equal(t, leavesBefore, probeA.leaveCount(), "Update must not call onMemberLeave")
	require.Greater(t, probeA.rebalances(), rebBefore)
}

func TestMembership_IsolateThenMerge(t *testing.T) {
	ports := allocGossipPorts(4)
	probeA := newCallbackProbe()

	a := newStaticNode(t, "iso-a", ports[0], nil, probeA)
	b := newStaticNode(t, "iso-b", ports[1], []string{a.gossip}, newCallbackProbe())
	a.start(t)
	b.start(t)
	requireSeesEventually(t, a, b)

	c := newStaticNode(t, "iso-c", ports[2], nil, newCallbackProbe())
	d := newStaticNode(t, "iso-d", ports[3], []string{c.gossip}, newCallbackProbe())
	c.start(t)
	d.start(t)
	requireSeesEventually(t, c, d)

	require.Never(t, func() bool {
		return a.mem.GetGrpcAddressForMember(c.id) != "" || c.mem.GetGrpcAddressForMember(a.id) != ""
	}, 400*time.Millisecond, 50*time.Millisecond)

	_, err := a.impl.mlist.Join([]string{c.gossip})
	require.NoError(t, err)

	requireSeesEventually(t, a, b, c, d)
	requireSeesEventually(t, c, a, b, d)
	requireRingAgreement(t, []*testNode{a, b, c, d}, 32)
}
