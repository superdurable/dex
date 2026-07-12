// Code generated from metered.tmpl. DO NOT EDIT.
package wrappers

import (
	"context"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/internal/metrics"
	"github.com/superdurable/dex/server/internal/persistence"
)

// ShardStoreWithMetrics instruments persistence.ShardStore with metrics
type ShardStoreWithMetrics struct {
	base   persistence.ShardStore
	logger log.Logger
}

// NewShardStoreWithMetrics creates a new metered wrapper
func NewShardStoreWithMetrics(base persistence.ShardStore, logger log.Logger) persistence.ShardStore {
	return &ShardStoreWithMetrics{base: base, logger: logger}
}

func (d *ShardStoreWithMetrics) ClaimShard(ctx context.Context, shardID int32, memberID string, leaseDuration time.Duration) (shard *persistence.Shard, err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("ShardStore.ClaimShard")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	shard, err = d.base.ClaimShard(ctx, shardID, memberID, leaseDuration)
	return
}

func (d *ShardStoreWithMetrics) RenewShardLease(ctx context.Context, shardID int32, memberID string, expectedVersion int64, leaseDuration time.Duration, metadata *persistence.ShardMetadata) (leaseExpiresAt time.Time, err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("ShardStore.RenewShardLease")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	leaseExpiresAt, err = d.base.RenewShardLease(ctx, shardID, memberID, expectedVersion, leaseDuration, metadata)
	return
}

func (d *ShardStoreWithMetrics) ReleaseShard(ctx context.Context, shardID int32, memberID string, expectedVersion int64) (err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("ShardStore.ReleaseShard")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	err = d.base.ReleaseShard(ctx, shardID, memberID, expectedVersion)
	return
}

func (d *ShardStoreWithMetrics) BatchReleaseShards(ctx context.Context, memberID string, entries []persistence.ShardReleaseEntry) (err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("ShardStore.BatchReleaseShards")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	err = d.base.BatchReleaseShards(ctx, memberID, entries)
	return
}

func (d *ShardStoreWithMetrics) Close() error {
	return d.base.Close()
}
