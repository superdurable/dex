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

// VisibilityStoreWithMetrics instruments persistence.VisibilityStore with metrics
type VisibilityStoreWithMetrics struct {
	base   persistence.VisibilityStore
	logger log.Logger
}

// NewVisibilityStoreWithMetrics creates a new metered wrapper
func NewVisibilityStoreWithMetrics(base persistence.VisibilityStore, logger log.Logger) persistence.VisibilityStore {
	return &VisibilityStoreWithMetrics{base: base, logger: logger}
}

func (d *VisibilityStoreWithMetrics) BatchUpsertVisibility(ctx context.Context, entries []persistence.VisibilityEntry) (err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("VisibilityStore.BatchUpsertVisibility")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	err = d.base.BatchUpsertVisibility(ctx, entries)
	return
}

func (d *VisibilityStoreWithMetrics) ListRuns(ctx context.Context, q persistence.ListRunsQuery) (result *persistence.ListRunsResult, err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("VisibilityStore.ListRuns")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	result, err = d.base.ListRuns(ctx, q)
	return
}

func (d *VisibilityStoreWithMetrics) DeleteAll(ctx context.Context) error {
	return d.base.DeleteAll(ctx)
}

func (d *VisibilityStoreWithMetrics) Close() error {
	return d.base.Close()
}
