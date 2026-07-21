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
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/log"
)

func TestLookupNewDNSAddress_TestFakeKeepsHostPort(t *testing.T) {
	ports := allocGossipPorts(1)
	gossip := gossipAddr(ports[0])
	dns := &fakeDNS{}
	dns.set() // empty at bootstrap so Start does not Join dead peers

	cfg := &config.MembershipConfig{
		BindAddress:           gossip,
		AdvertiseAddress:      gossip,
		NumberOfVNodes:        8,
		MinMembersBeforeReady: 1,
		Discovery: config.DiscoveryConfig{
			Mode:               "dns",
			DNSAddress:         "svc.local",
			DNSPort:            9999, // must not rewrite fake host:port
			DNSRefreshInterval: 1e9,
		},
	}
	mem := newMembershipWithLookup(
		cfg, log.NewDefaultLogger(), "lookup-a", "10.0.1.1:7233",
		func() {}, func(string) {}, dns.lookup,
	).(*membershipImpl)
	require.NoError(t, mem.Start())
	t.Cleanup(mem.Stop)

	dns.set("10.0.1.5:7946", "10.0.1.6:7946")
	addrs, err := mem.lookupNewDNSAddress()
	require.NoError(t, err)
	require.Equal(t, []string{"10.0.1.5:7946", "10.0.1.6:7946"}, addrs)
}

func TestLookupNewDNSAddress_FiltersAlreadyConnected(t *testing.T) {
	ports := allocGossipPorts(1)
	gossip := gossipAddr(ports[0])
	dns := &fakeDNS{}
	dns.set()

	cfg := &config.MembershipConfig{
		BindAddress:           gossip,
		AdvertiseAddress:      gossip,
		NumberOfVNodes:        8,
		MinMembersBeforeReady: 1,
		Discovery: config.DiscoveryConfig{
			Mode:               "dns",
			DNSAddress:         "svc.local",
			DNSPort:            ports[0],
			DNSRefreshInterval: 1e9,
		},
	}
	mem := newMembershipWithLookup(
		cfg, log.NewDefaultLogger(), "lookup-b", "127.0.0.1:7233",
		func() {}, func(string) {}, dns.lookup,
	).(*membershipImpl)
	require.NoError(t, mem.Start())
	t.Cleanup(mem.Stop)

	dns.set(gossip, fmt.Sprintf("10.0.0.9:%d", ports[0]))
	addrs, err := mem.lookupNewDNSAddress()
	require.NoError(t, err)
	require.Equal(t, []string{fmt.Sprintf("10.0.0.9:%d", ports[0])}, addrs)
}
