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

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/server/config"
)

func TestBuildDiscoveryTargets_UsesBindPortAndFiltersSelf(t *testing.T) {
	cfg := config.MembershipConfig{
		BindAddress:      "0.0.0.0:7946",
		AdvertiseAddress: "10.0.0.1:7946",
		Discovery: config.DiscoveryConfig{
			Mode:       "dns",
			ServiceDNS: "dex-headless.default.svc.cluster.local",
		},
	}

	targets := buildDiscoveryTargets(&cfg, []string{"10.0.0.1", "10.0.0.2", "10.0.0.2", "10.0.0.3"})
	require.Equal(t, []string{"10.0.0.2:7946", "10.0.0.3:7946"}, targets)
}

func TestBuildDiscoveryTargets_UsesOverridePort(t *testing.T) {
	cfg := config.MembershipConfig{
		BindAddress: "0.0.0.0:7946",
		Discovery: config.DiscoveryConfig{
			Mode:       "dns",
			ServiceDNS: "dex-headless.default.svc.cluster.local",
			Port:       9000,
		},
	}

	targets := buildDiscoveryTargets(&cfg, []string{"10.0.0.2"})
	require.Equal(t, []string{"10.0.0.2:9000"}, targets)
}

func TestResolveInternalAddress_UsesAdvertiseHost(t *testing.T) {
	resolved := resolveInternalAddress("10.0.1.5:7946", "127.0.0.1:7233")
	require.Equal(t, "10.0.1.5:7233", resolved)
}

func TestResolveInternalAddress_KeepsInternalWhenNoAdvertise(t *testing.T) {
	resolved := resolveInternalAddress("", "127.0.0.1:7233")
	require.Equal(t, "127.0.0.1:7233", resolved)
}
