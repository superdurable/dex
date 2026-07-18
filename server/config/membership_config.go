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

package config

import "time"

type MembershipConfig struct {
	// BindAddress is the local gossip bind address (UDP/TCP). Default: "0.0.0.0:7946".
	BindAddress string `yaml:"bindAddress"`
	// AdvertiseAddress is the gossip address peers dial. Required in containers (pod IP).
	// Example: "10.0.1.5:7946". If empty, BindAddress is used.
	AdvertiseAddress string `yaml:"advertiseAddress"`
	// AdvertiseGRPCAddress is the gRPC dial target peers use for forwarding.
	// Broadcast in memberlist Meta. Default: derived from GRPCListenAddress.
	// Example: "10.0.1.5:7233".
	AdvertiseGRPCAddress string `yaml:"advertiseGrpcAddress"`
	// StaticAddresses are gossip seed peers to Join on startup. Default: empty (first node).
	StaticAddresses []string `yaml:"staticAddresses"`
	// NumberOfVNodes is virtual nodes per member on the hash ring. Default: 128.
	// Higher is more even ownership; immutable after cluster creation.
	NumberOfVNodes int `yaml:"numberOfVNodes"`
	// MinMembersBeforeReady blocks ready until N members join. Default: 2. Set 1 to disable.
	MinMembersBeforeReady int `yaml:"minMembersBeforeReady"`
	// ClaimRetryInterval is how often to retry a blocked ownership claim. Default: 10s.
	ClaimRetryInterval time.Duration `yaml:"claimRetryInterval"`
	// ClaimRetryIntervalJitter is random jitter added to ClaimRetryInterval. Default: 2s.
	ClaimRetryIntervalJitter time.Duration `yaml:"claimRetryIntervalJitter"`

	// Discovery controls how this node finds peer seed addresses.
	Discovery DiscoveryConfig `yaml:"discovery"`

	// OwnershipOpsMaxAttempts is Claim*/Renew* attempts on transient store errors.
	// Includes the first try. CAS mismatches are never retried. Default: 3.
	OwnershipOpsMaxAttempts int `yaml:"ownershipOpsMaxAttempts"`
}

// DiscoveryConfig controls how a memberlist cluster discovers seed nodes.
type DiscoveryConfig struct {
	// Mode is "static" (StaticAddresses only) or "dns". Default: "static".
	Mode string `yaml:"mode"`

	// ServiceDNS is the headless DNS name to resolve when Mode is "dns".
	ServiceDNS string `yaml:"serviceDns"`

	// Port is the gossip port for DNS targets. Default: 0 (use BindAddress port).
	Port int `yaml:"port"`

	// RefreshInterval is how often DNS discovery re-resolves. Default: 30s.
	RefreshInterval time.Duration `yaml:"refreshInterval"`
}

// DefaultDiscoveryConfig returns discovery defaults (static mode, 30s refresh).
func DefaultDiscoveryConfig() DiscoveryConfig {
	return DiscoveryConfig{
		Mode:            "static",
		RefreshInterval: 30 * time.Second,
	}
}
