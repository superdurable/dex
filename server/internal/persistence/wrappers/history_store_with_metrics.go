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

// HistoryStoreWithMetrics instruments persistence.HistoryStore with metrics
type HistoryStoreWithMetrics struct {
	base   persistence.HistoryStore
	logger log.Logger
}

// NewHistoryStoreWithMetrics creates a new metered wrapper
func NewHistoryStoreWithMetrics(base persistence.HistoryStore, logger log.Logger) persistence.HistoryStore {
	return &HistoryStoreWithMetrics{base: base, logger: logger}
}

func (d *HistoryStoreWithMetrics) BatchInsertHistory(ctx context.Context, events []persistence.HistoryEvent) (err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("HistoryStore.BatchInsertHistory")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	err = d.base.BatchInsertHistory(ctx, events)
	return
}

func (d *HistoryStoreWithMetrics) GetHistoryEvents(ctx context.Context, namespace, runID string, afterID int64, limit int) (out []persistence.HistoryEvent, err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("HistoryStore.GetHistoryEvents")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	out, err = d.base.GetHistoryEvents(ctx, namespace, runID, afterID, limit)
	return
}

func (d *HistoryStoreWithMetrics) GetLatestEvent(ctx context.Context, namespace, runID string) (out *persistence.HistoryEvent, err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("HistoryStore.GetLatestEvent")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	out, err = d.base.GetLatestEvent(ctx, namespace, runID)
	return
}

func (d *HistoryStoreWithMetrics) DeleteAll(ctx context.Context) error {
	return d.base.DeleteAll(ctx)
}

func (d *HistoryStoreWithMetrics) Close() error {
	return d.base.Close()
}
