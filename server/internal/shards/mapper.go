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

package shards

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/errors"
)

// ShardMapper maps (namespace, runID) to a shard ID.
// numShards is per-namespace and immutable once used.
type ShardMapper interface {
	GetShardID(namespace string, runID string) int32
	GetNumShards(namespace string) int32
	// ValidateNamespaceValidForMagicSharding checks a client namespace is safe
	// under magic sharding (parsed n must be <= TotalShards).
	ValidateNamespaceValidForMagicSharding(namespace string) errors.CategorizedError
}

type shardMapperImpl struct {
	cfg *config.ShardConfig
}

func NewShardMapper(cfg *config.ShardConfig) ShardMapper {
	if cfg == nil {
		panic("ShardConfig must not be nil")
	}
	if cfg.TotalShards <= 0 {
		panic("TotalShards must be > 0")
	}
	if cfg.DefaultShardsForNewNamespaces <= 0 {
		panic("DefaultShardsForNewNamespaces must be > 0")
	}
	if cfg.DefaultShardsForNewNamespaces > cfg.TotalShards {
		panic(fmt.Sprintf("DefaultShardsForNewNamespaces (%d) exceeds TotalShards (%d)",
			cfg.DefaultShardsForNewNamespaces, cfg.TotalShards))
	}
	for namespace, numShards := range cfg.NamespaceShardsRegistry {
		if numShards <= 0 {
			panic("NamespaceShardsRegistry values must be > 0")
		}
		if numShards > cfg.TotalShards {
			panic(fmt.Sprintf("NamespaceShardsRegistry[%q]=%d exceeds TotalShards (%d)",
				namespace, numShards, cfg.TotalShards))
		}
	}
	return &shardMapperImpl{cfg: cfg}
}

func (m *shardMapperImpl) GetShardID(namespace string, runID string) int32 {
	numShards := m.GetNumShards(namespace)
	hashed := xxhash.Sum64String(runID)
	return int32(hashed % uint64(numShards))
}

func (m *shardMapperImpl) GetNumShards(namespace string) int32 {
	var numShards int32
	if m.cfg.EnableMagicShardsByNamespacePrefix {
		if n, ok := parseMagicShardCount(namespace); ok {
			numShards = n
		}
	}
	if numShards == 0 {
		if n, ok := m.cfg.NamespaceShardsRegistry[namespace]; ok {
			numShards = int32(n)
		} else {
			numShards = int32(m.cfg.DefaultShardsForNewNamespaces)
		}
	}
	if int(numShards) > m.cfg.TotalShards {
		panic(fmt.Sprintf("namespace %q numShards (%d) exceeds TotalShards (%d)",
			namespace, numShards, m.cfg.TotalShards))
	}
	return numShards
}

func (m *shardMapperImpl) ValidateNamespaceValidForMagicSharding(namespace string) errors.CategorizedError {
	if !m.cfg.EnableMagicShardsByNamespacePrefix {
		return nil
	}
	n, ok := parseMagicShardCount(namespace)
	if !ok {
		return nil
	}
	if int(n) > m.cfg.TotalShards {
		return errors.NewInvalidInputError(
			fmt.Sprintf("magic namespace %q numShards (%d) exceeds TotalShards (%d)",
				namespace, n, m.cfg.TotalShards),
			nil,
		)
	}
	return nil
}

// parseMagicShardCount parses "<n>_shards_<xyz>" into n. namespace is untrusted;
// bitSize 32 rejects overflow so a bogus prefix can't yield a negative count.
func parseMagicShardCount(namespace string) (int32, bool) {
	prefix, rest, ok := strings.Cut(namespace, "_shards_")
	if !ok || prefix == "" || rest == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(prefix, 10, 32)
	if err != nil || n <= 0 {
		return 0, false
	}
	return int32(n), true
}
