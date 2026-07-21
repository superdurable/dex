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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMembership_StaticJoinAndLeaveToSingle(t *testing.T) {
	ports := allocGossipPorts(3)
	probeA, probeB, probeC := newCallbackProbe(), newCallbackProbe(), newCallbackProbe()

	a := newStaticNode(t, "static-a", ports[0], nil, probeA)
	a.start(t)

	b := newStaticNode(t, "static-b", ports[1], []string{a.gossip}, probeB)
	b.start(t)
	c := newStaticNode(t, "static-c", ports[2], []string{a.gossip}, probeC)
	c.start(t)

	requireSeesEventually(t, a, b, c)
	requireSeesEventually(t, b, a, c)
	requireSeesEventually(t, c, a, b)
	require.GreaterOrEqual(t, probeA.rebalances(), 1)
	requireRingAgreement(t, []*testNode{a, b, c}, 32)

	c.mem.Stop()
	requireLeaveContains(t, probeA, c.grpc)
	requireLeaveContains(t, probeB, c.grpc)
	requireNotSeeEventually(t, a, c.id)
	requireNotSeeEventually(t, b, c.id)
	requireSeesEventually(t, a, b)
	requireRingAgreement(t, []*testNode{a, b}, 32)

	b.mem.Stop()
	requireLeaveContains(t, probeA, b.grpc)
	requireNotSeeEventually(t, a, b.id)
	require.Equal(t, a.grpc, a.mem.GetGrpcAddressForMember(a.id))
	require.Equal(t, "", a.mem.GetGrpcAddressForMember(b.id))
}

func TestMembership_DNS_EmptyThenGrow(t *testing.T) {
	ports := allocGossipPorts(3)
	dns := &fakeDNS{}
	dns.set()
	probeA, probeB, probeC := newCallbackProbe(), newCallbackProbe(), newCallbackProbe()

	a := newDNSNode(t, "dns-a", ports[0], dns, probeA)
	a.start(t)
	require.Equal(t, "", a.mem.GetGrpcAddressForMember("dns-b"))

	dns.set(gossipAddr(ports[1]))
	b := newDNSNode(t, "dns-b", ports[1], dns, probeB)
	b.start(t)
	requireSeesEventually(t, a, b)
	requireSeesEventually(t, b, a)
	require.GreaterOrEqual(t, probeA.rebalances(), 1)

	dns.set(gossipAddr(ports[1]), gossipAddr(ports[2]))
	c := newDNSNode(t, "dns-c", ports[2], dns, probeC)
	c.start(t)
	requireSeesEventually(t, a, b, c)
	requireSeesEventually(t, b, a, c)
	requireSeesEventually(t, c, a, b)
	requireRingAgreement(t, []*testNode{a, b, c}, 32)
}

func TestMembership_DNS_SeedThenGrow(t *testing.T) {
	ports := allocGossipPorts(3)
	dns := &fakeDNS{}
	dns.set(gossipAddr(ports[1]))
	probeA, probeB, probeC := newCallbackProbe(), newCallbackProbe(), newCallbackProbe()

	a := newDNSNode(t, "seed-a", ports[0], dns, probeA)
	b := newDNSNode(t, "seed-b", ports[1], dns, probeB)
	b.start(t)
	a.start(t)
	requireSeesEventually(t, a, b)
	requireSeesEventually(t, b, a)
	rebAfterTwo := probeA.rebalances()

	dns.set(gossipAddr(ports[1]), gossipAddr(ports[2]))
	c := newDNSNode(t, "seed-c", ports[2], dns, probeC)
	c.start(t)
	requireSeesEventually(t, a, b, c)
	require.Greater(t, probeA.rebalances(), rebAfterTwo)
	requireRingAgreement(t, []*testNode{a, b, c}, 32)
}

func TestMembership_DNS_MidLeaveAndLeaveToSingle(t *testing.T) {
	ports := allocGossipPorts(3)
	dns := &fakeDNS{}
	dns.set()
	probeA, probeB, probeC := newCallbackProbe(), newCallbackProbe(), newCallbackProbe()

	a := newDNSNode(t, "leave-a", ports[0], dns, probeA)
	b := newDNSNode(t, "leave-b", ports[1], dns, probeB)
	c := newDNSNode(t, "leave-c", ports[2], dns, probeC)
	a.start(t)
	b.start(t)
	c.start(t)
	dns.set(gossipAddr(ports[0]), gossipAddr(ports[1]), gossipAddr(ports[2]))
	requireSeesEventually(t, a, b, c)
	requireSeesEventually(t, b, a, c)

	b.mem.Stop()
	requireLeaveContains(t, probeA, b.grpc)
	requireLeaveContains(t, probeC, b.grpc)
	requireNotSeeEventually(t, a, b.id)
	requireSeesEventually(t, a, c)
	requireRingAgreement(t, []*testNode{a, c}, 32)

	dns.set(gossipAddr(ports[0]), gossipAddr(ports[1]), gossipAddr(ports[2]))
	require.Never(t, func() bool {
		return a.mem.GetGrpcAddressForMember(b.id) != ""
	}, 400*time.Millisecond, 50*time.Millisecond)

	c.mem.Stop()
	requireLeaveContains(t, probeA, c.grpc)
	requireNotSeeEventually(t, a, c.id)
	require.Equal(t, "", a.mem.GetGrpcAddressForMember(c.id))
}
