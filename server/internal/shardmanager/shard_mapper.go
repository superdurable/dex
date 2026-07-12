package shardmanager

import (
	"hash/crc32"

	"github.com/superdurable/dex/server/config"
)

// ShardMapper maps (namespace, runID) to a shard ID.
// numShards is per-namespace and immutable once created.
type ShardMapper interface {
	GetShardID(namespace string, runID string) int32
	GetNumShards(namespace string) int32
}

type shardMapperImpl struct {
	defaultShards int32
	namespaces    map[string]int32 // namespace -> numShards overrides
}

func NewShardMapper(cfg config.ShardConfig) ShardMapper {
	if cfg.DefaultShardsForNewNamespaces <= 0 {
		panic("DefaultShardsForNewNamespaces must be > 0")
	}
	ns := make(map[string]int32, len(cfg.NamespaceShardsRegistry))
	for k, v := range cfg.NamespaceShardsRegistry {
		ns[k] = int32(v)
	}
	return &shardMapperImpl{
		defaultShards: int32(cfg.DefaultShardsForNewNamespaces),
		namespaces:    ns,
	}
}

func (m *shardMapperImpl) GetShardID(namespace string, runID string) int32 {
	numShards := m.GetNumShards(namespace)
	h := crc32.ChecksumIEEE([]byte(runID))
	return int32(h % uint32(numShards))
}

func (m *shardMapperImpl) GetNumShards(namespace string) int32 {
	if n, ok := m.namespaces[namespace]; ok {
		return n
	}
	return m.defaultShards
}
