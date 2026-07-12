// Code generated from metered.tmpl. DO NOT EDIT.
package wrappers

import (
	"context"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/internal/metrics"
	"github.com/superdurable/dex/server/internal/persistence"
)

// BlobStoreWithMetrics instruments persistence.BlobStore with metrics
type BlobStoreWithMetrics struct {
	base   persistence.BlobStore
	logger log.Logger
}

// NewBlobStoreWithMetrics creates a new metered wrapper
func NewBlobStoreWithMetrics(base persistence.BlobStore, logger log.Logger) persistence.BlobStore {
	return &BlobStoreWithMetrics{base: base, logger: logger}
}

func (d *BlobStoreWithMetrics) BatchInsertBlobs(ctx context.Context, shardID int32, namespace, runID string, blobs []persistence.BlobEntry) (err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("BlobStore.BatchInsertBlobs")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	err = d.base.BatchInsertBlobs(ctx, shardID, namespace, runID, blobs)
	return
}

func (d *BlobStoreWithMetrics) BatchGetBlobs(ctx context.Context, shardID int32, namespace, runID string, blobIDs []ids.BlobID) (blobs []persistence.BlobEntry, err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("BlobStore.BatchGetBlobs")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	blobs, err = d.base.BatchGetBlobs(ctx, shardID, namespace, runID, blobIDs)
	return
}

func (d *BlobStoreWithMetrics) Close() error {
	return d.base.Close()
}
