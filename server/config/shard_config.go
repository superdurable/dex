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

type ShardConfig struct {
	// Membership is gossip/membership settings (single-node is a 1-member cluster).
	Membership MembershipConfig `yaml:"membership"`
	// TotalShards is the cluster-wide shard count (one owner per shard).
	// May increase, never decrease. Must be >= max(namespace.numShards).
	// Start around 10x expected max members.
	TotalShards int `yaml:"maxShards"`
	// DefaultShardsForNewNamespaces is numShards for namespaces unlisted below. Default: 2.
	DefaultShardsForNewNamespaces int `yaml:"defaultShardsForNewNamespaces"`
	// NamespaceShardsRegistry overrides numShards per namespace.
	// Only add unused namespaces; changing remaps runs and breaks ownership.
	NamespaceShardsRegistry map[string]int `yaml:"namespaceShardsRegistry"`
	// EnableMagicShardsByNamespacePrefix will decide namespace shard count by prefix:
	//   <n>_shards_<xyz> will use n shards
	// NOTE: be extra careful when flipping this flag for existing namespaces
	// Default: true
	EnableMagicShardsByNamespacePrefix bool `yaml:"enableMagicShardsByNamespacePrefix"`
	// LeaseDuration is the shard lease TTL; others steal after expiry. Default: 30s.
	LeaseDuration time.Duration `yaml:"leaseDuration"`
	// LeaseRenewInterval is how often the owner renews (and commits watermarks).
	// Must be well below LeaseDuration. Default: 10s.
	LeaseRenewInterval time.Duration `yaml:"leaseRenewInterval"`
	// LeaseRenewJitter is random jitter on renewals to avoid stampedes. Default: 2s.
	LeaseRenewJitter time.Duration `yaml:"leaseRenewJitter"`
	// LeaseExpiryBuffer is subtracted from lease expiry for op deadlines.
	// Leaves a safety gap so in-flight writes finish before steal. Default: 5s.
	LeaseExpiryBuffer time.Duration `yaml:"leaseExpiryBuffer"`
	// ShutdownGracefulPeriod waits after stopping shard work before ReleaseShard.
	// Require GracefulPeriod + LeaseExpiryBuffer < LeaseDuration. Default: 5s.
	ShutdownGracefulPeriod time.Duration `yaml:"shutdownGracefulPeriod"`
}
