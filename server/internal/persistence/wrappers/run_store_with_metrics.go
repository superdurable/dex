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

// RunStoreWithMetrics instruments persistence.RunStore with metrics
type RunStoreWithMetrics struct {
	base   persistence.RunStore
	logger log.Logger
}

// NewRunStoreWithMetrics creates a new metered wrapper
func NewRunStoreWithMetrics(base persistence.RunStore, logger log.Logger) persistence.RunStore {
	return &RunStoreWithMetrics{base: base, logger: logger}
}

func (d *RunStoreWithMetrics) CreateRunWithTasks(ctx context.Context, run *persistence.RunRow, tasks []persistence.TaskRow) (err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("RunStore.CreateRunWithTasks")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	err = d.base.CreateRunWithTasks(ctx, run, tasks)
	return
}

func (d *RunStoreWithMetrics) GetRun(ctx context.Context, shardID int32, namespace, runID string, opts persistence.GetRunOptions) (row *persistence.RunRow, err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("RunStore.GetRun")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	row, err = d.base.GetRun(ctx, shardID, namespace, runID, opts)
	return
}

func (d *RunStoreWithMetrics) UpdateRunWithNewTasks(ctx context.Context, shardID int32, namespace, runID string, expectedVersion int64, update *persistence.RunRowUpdate, newTasks []persistence.TaskRow) (err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("RunStore.UpdateRunWithNewTasks")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	err = d.base.UpdateRunWithNewTasks(ctx, shardID, namespace, runID, expectedVersion, update, newTasks)
	return
}

func (d *RunStoreWithMetrics) RangeReadImmediateTasks(ctx context.Context, shardID int32, afterSeq int64, limit int) (tasks []*persistence.ImmediateTaskRow, err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("RunStore.RangeReadImmediateTasks")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	tasks, err = d.base.RangeReadImmediateTasks(ctx, shardID, afterSeq, limit)
	return
}

func (d *RunStoreWithMetrics) RangeReadTimerTasks(ctx context.Context, shardID int32, sortKeyUpTo int64, afterSortKey int64, afterID ids.TaskID, limit int) (tasks []*persistence.TimerTaskRow, err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("RunStore.RangeReadTimerTasks")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	tasks, err = d.base.RangeReadTimerTasks(ctx, shardID, sortKeyUpTo, afterSortKey, afterID, limit)
	return
}

func (d *RunStoreWithMetrics) RangeDeleteImmediateTasks(ctx context.Context, shardID int32, upToSeq int64) (err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("RunStore.RangeDeleteImmediateTasks")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	err = d.base.RangeDeleteImmediateTasks(ctx, shardID, upToSeq)
	return
}

func (d *RunStoreWithMetrics) RangeDeleteTimerTasks(ctx context.Context, shardID int32, upToSortKey int64, upToID ids.TaskID) (err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("RunStore.RangeDeleteTimerTasks")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	err = d.base.RangeDeleteTimerTasks(ctx, shardID, upToSortKey, upToID)
	return
}

func (d *RunStoreWithMetrics) DeleteImmediateTasksByIDBatch(ctx context.Context, shardID int32, taskIDs []ids.TaskID) (err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("RunStore.DeleteImmediateTasksByIDBatch")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	err = d.base.DeleteImmediateTasksByIDBatch(ctx, shardID, taskIDs)
	return
}

func (d *RunStoreWithMetrics) DeleteTimerTasksByIDBatch(ctx context.Context, shardID int32, taskIDs []ids.TaskID) (err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("RunStore.DeleteTimerTasksByIDBatch")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	err = d.base.DeleteTimerTasksByIDBatch(ctx, shardID, taskIDs)
	return
}

func (d *RunStoreWithMetrics) RangeReadOpsFIFOTasks(ctx context.Context, shardID int32, afterSeq int64, limit int) (tasks []*persistence.OpsFIFOTaskRow, err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("RunStore.RangeReadOpsFIFOTasks")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	tasks, err = d.base.RangeReadOpsFIFOTasks(ctx, shardID, afterSeq, limit)
	return
}

func (d *RunStoreWithMetrics) RangeDeleteOpsFIFOTasks(ctx context.Context, shardID int32, upToSeq int64) (err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("RunStore.RangeDeleteOpsFIFOTasks")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	err = d.base.RangeDeleteOpsFIFOTasks(ctx, shardID, upToSeq)
	return
}

func (d *RunStoreWithMetrics) DeleteOpsFIFOTasksByIDBatch(ctx context.Context, shardID int32, taskIDs []ids.TaskID) (err errors.CategorizedError) {
	since := time.Now()
	defer func() {
		methodTag := metrics.TagPersistenceMethodName("RunStore.DeleteOpsFIFOTasksByIDBatch")
		if err != nil {
			metrics.CounterStoreMethodError.Inc(methodTag, metrics.TagErrorCategoryFromCategorizedError(err))
		} else {
			metrics.LatencyStoreMethod.Record(time.Since(since), methodTag)
		}
	}()
	err = d.base.DeleteOpsFIFOTasksByIDBatch(ctx, shardID, taskIDs)
	return
}

func (d *RunStoreWithMetrics) DeleteAll(ctx context.Context) error {
	return d.base.DeleteAll(ctx)
}

func (d *RunStoreWithMetrics) Close() error {
	return d.base.Close()
}
