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

package taskprocessor

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"common-go/ids"

	"github.com/google/btree"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/log"
	"github.com/superdurable/dex/server/internal/log/tag"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shards"
)

// TimerTaskDeleter tracks in-flight timer tasks.
// GetWatermark is the reclaim cursor (reads strictly after it) and never equals
// an in-flight key. RangeDelete uses min(pending) or nextAfter(maxCompleted).
type TimerTaskDeleter interface {
	Start(ctx context.Context)
	Stop(forced bool)
	DoneCh() chan<- TaskCompletion
	InsertPending(sortKey int64, id ids.UID)
	RemovePending(sortKey int64, id ids.UID)
	GetWatermark() (sortKey int64, id ids.UID)
}

type timerTaskDeleterImpl struct {
	cfg      *config.TaskProcessorConfig
	shardCfg *config.ShardConfig
	store    p.RunStore
	sm       shards.ShardManager
	shardID  int32
	logger   log.Logger

	doneCh chan TaskCompletion

	mu             sync.Mutex
	pending        *btree.BTreeG[timerTaskKey]
	maxCompleted   timerTaskKey // inclusive; published via GetWatermark
	deleteUpTo     timerTaskKey // exclusive high-water of successful RangeDelete
	completedAbove map[ids.UID]timerTaskKey

	cancel       context.CancelFunc
	wg           sync.WaitGroup
	shuttingDown atomic.Bool
}

var _ TimerTaskDeleter = (*timerTaskDeleterImpl)(nil)

func (d *timerTaskDeleterImpl) opCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if d.shuttingDown.Load() {
		return context.WithCancel(ctx)
	}
	return d.sm.GetCappedContext(ctx, d.shardID)
}

// NewTimerTaskDeleter starts from the shard's committed watermark.
func NewTimerTaskDeleter(
	cfg *config.TaskProcessorConfig,
	shardCfg *config.ShardConfig,
	store p.RunStore,
	sm shards.ShardManager,
	shardID int32,
	initialSortKey int64,
	initialID ids.UID,
	logger log.Logger,
) TimerTaskDeleter {
	if cfg == nil {
		panic("TaskProcessorConfig must not be nil")
	}
	if shardCfg == nil {
		panic("ShardConfig must not be nil")
	}
	if store == nil {
		panic("RunStore must not be nil")
	}
	if sm == nil {
		panic("ShardManager must not be nil")
	}
	if logger == nil {
		panic("Logger must not be nil")
	}
	if cfg.NumWorkers <= 0 {
		panic("TaskProcessorConfig.NumWorkers must be > 0")
	}
	if cfg.TimerDeleteInterval <= 0 {
		panic("TaskProcessorConfig.TimerDeleteInterval must be > 0")
	}

	initial := timerTaskKey{sortKey: initialSortKey, id: initialID}
	return &timerTaskDeleterImpl{
		cfg:            cfg,
		shardCfg:       shardCfg,
		store:          store,
		sm:             sm,
		shardID:        shardID,
		logger:         logger,
		doneCh:         make(chan TaskCompletion, cfg.NumWorkers*2),
		pending:        btree.NewG(32, timerTaskKeyLess),
		maxCompleted:   initial,
		deleteUpTo:     initial,
		completedAbove: make(map[ids.UID]timerTaskKey),
	}
}

func (d *timerTaskDeleterImpl) Start(ctx context.Context) {
	if d.cancel != nil {
		panic("TimerTaskDeleter already started")
	}
	ctx, d.cancel = context.WithCancel(ctx)
	d.wg.Add(1)
	go d.run(ctx)
}

// Stop halts the deleter. forced=true (ownership lost) skips store cleanup: the
// generation is already fenced, so any delete here would be unfenced and could
// remove the new owner's tasks; the new owner re-reads instead. forced=false
// (voluntary release, lease still held) flushes committed deletions.
func (d *timerTaskDeleterImpl) Stop(forced bool) {
	if d.cancel == nil {
		return
	}
	d.shuttingDown.Store(true)
	d.cancel()
	d.wg.Wait()
	d.cancel = nil

	if forced {
		return
	}

	ctx, cancel := d.shutdownCtx()
	defer cancel()
	d.drainDone(ctx)
	d.tryAdvance(ctx)
	d.flushCompletedAbove(ctx)
}

func (d *timerTaskDeleterImpl) shutdownCtx() (context.Context, context.CancelFunc) {
	if d.shardCfg.ShutdownGracefulPeriod <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), d.shardCfg.ShutdownGracefulPeriod)
}

func (d *timerTaskDeleterImpl) run(ctx context.Context) {
	defer d.wg.Done()

	timer := time.NewTimer(withJitter(d.cfg.TimerDeleteInterval, d.cfg.TimerDeleteIntervalJitter))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case completion := <-d.doneCh:
			d.onComplete(completion)
		case <-timer.C:
			d.tryAdvance(ctx)
			timer.Reset(withJitter(d.cfg.TimerDeleteInterval, d.cfg.TimerDeleteIntervalJitter))
		}
	}
}

func (d *timerTaskDeleterImpl) drainDone(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case completion := <-d.doneCh:
			d.onComplete(completion)
		default:
			// Re-check after tryAdvance: completions may have arrived during it.
			d.tryAdvance(ctx)
			select {
			case completion := <-d.doneCh:
				d.onComplete(completion)
			default:
				return
			}
		}
	}
}

func (d *timerTaskDeleterImpl) onComplete(completion TaskCompletion) {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := timerTaskKey{sortKey: completion.SortKey, id: completion.ID}
	if _, ok := d.pending.Delete(key); !ok {
		d.logger.Error("timer task completion for unknown pending task",
			tag.ShardId(d.shardID),
		)
		return
	}
	if timerTaskKeyLess(d.maxCompleted, key) {
		d.maxCompleted = key
	}
	// Exclusive RangeDelete misses keys >= deleteUpTo; keep for by-ID delete.
	if !timerTaskKeyLess(key, d.deleteUpTo) {
		d.completedAbove[key.id] = key
	}
}

func (d *timerTaskDeleterImpl) tryAdvance(ctx context.Context) {
	for {
		d.mu.Lock()
		bound := d.deleteBoundLocked()
		if !timerTaskKeyLess(d.deleteUpTo, bound) {
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()

		capped, cancel := d.opCtx(ctx)
		err := d.store.RangeDeleteTimerTasks(capped, d.shardID, bound.sortKey, bound.id)
		cancel()
		if err != nil {
			d.logger.Error("range delete timer tasks failed",
				tag.ShardId(d.shardID),
				tag.Error(err),
			)
			return
		}

		d.mu.Lock()
		if minKey, ok := d.pending.Min(); ok && timerTaskKeyLess(minKey, bound) {
			d.mu.Unlock()
			d.logger.Error("pending timer task below delete watermark",
				tag.ShardId(d.shardID),
			)
			return
		}
		if timerTaskKeyLess(d.deleteUpTo, bound) {
			d.deleteUpTo = bound
			d.pruneCompletedAboveLocked(bound)
		}
		again := d.deleteBoundLocked()
		more := timerTaskKeyLess(bound, again)
		d.mu.Unlock()
		if !more {
			return
		}
	}
}

// deleteBoundLocked is the exclusive RangeDelete upper bound.
// Pending non-empty: min(pending). Empty: nextAfter(maxCompleted).
func (d *timerTaskDeleterImpl) deleteBoundLocked() timerTaskKey {
	if minKey, ok := d.pending.Min(); ok {
		return minKey
	}
	return nextTimerTaskKey(d.maxCompleted)
}

func (d *timerTaskDeleterImpl) flushCompletedAbove(ctx context.Context) {
	d.mu.Lock()
	if len(d.completedAbove) == 0 {
		d.mu.Unlock()
		return
	}
	uids := make([]ids.UID, 0, len(d.completedAbove))
	for id := range d.completedAbove {
		uids = append(uids, id)
	}
	d.mu.Unlock()

	batchSize := d.cfg.ShutdownDeleteBatchSize
	if batchSize <= 0 {
		batchSize = 1000
	}

	for start := 0; start < len(uids); start += batchSize {
		end := start + batchSize
		if end > len(uids) {
			end = len(uids)
		}
		page := uids[start:end]

		capped, cancel := d.opCtx(ctx)
		err := d.store.DeleteTimerTasksByIDBatch(capped, d.shardID, page)
		cancel()
		if err != nil {
			d.logger.Error("delete completed-above timer tasks failed",
				tag.ShardId(d.shardID),
				tag.Error(err),
			)
			return
		}

		d.mu.Lock()
		for _, id := range page {
			delete(d.completedAbove, id)
		}
		d.mu.Unlock()
	}
}

func (d *timerTaskDeleterImpl) pruneCompletedAboveLocked(exclusiveUpper timerTaskKey) {
	for id, key := range d.completedAbove {
		if timerTaskKeyLess(key, exclusiveUpper) {
			delete(d.completedAbove, id)
		}
	}
}

func (d *timerTaskDeleterImpl) DoneCh() chan<- TaskCompletion {
	return d.doneCh
}

func (d *timerTaskDeleterImpl) InsertPending(sortKey int64, id ids.UID) {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := timerTaskKey{sortKey: sortKey, id: id}
	if !timerTaskKeyLess(d.deleteUpTo, key) {
		panic("InsertPending key must be above delete high-water")
	}
	if _, existed := d.pending.ReplaceOrInsert(key); existed {
		panic("InsertPending duplicate timer task key")
	}
}

func (d *timerTaskDeleterImpl) RemovePending(sortKey int64, id ids.UID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending.Delete(timerTaskKey{sortKey: sortKey, id: id})
}

func (d *timerTaskDeleterImpl) GetWatermark() (sortKey int64, id ids.UID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Reclaim reads strictly after this key. Never publish an in-flight key:
	// when pending is non-empty, publish predecessor(min) so min is re-readable.
	if minKey, ok := d.pending.Min(); ok {
		prev := prevTimerTaskKey(minKey)
		return prev.sortKey, prev.id
	}
	return d.maxCompleted.sortKey, d.maxCompleted.id
}

type timerTaskKey struct {
	sortKey int64
	id      ids.UID
}

func timerTaskKeyLess(a, b timerTaskKey) bool {
	if a.sortKey != b.sortKey {
		return a.sortKey < b.sortKey
	}
	return a.id.Compare(b.id) < 0
}

func nextTimerTaskKey(key timerTaskKey) timerTaskKey {
	u := key.id.UUID()
	for i := 15; i >= 0; i-- {
		u[i]++
		if u[i] != 0 {
			return timerTaskKey{sortKey: key.sortKey, id: ids.UID(u)}
		}
	}
	return timerTaskKey{sortKey: key.sortKey + 1, id: ids.EmptyUId()}
}

func prevTimerTaskKey(key timerTaskKey) timerTaskKey {
	u := key.id.UUID()
	for i := 15; i >= 0; i-- {
		if u[i] > 0 {
			u[i]--
			for j := i + 1; j < 16; j++ {
				u[j] = 0xff
			}
			return timerTaskKey{sortKey: key.sortKey, id: ids.UID(u)}
		}
	}
	var maxID ids.UID
	for i := range maxID {
		maxID[i] = 0xff
	}
	return timerTaskKey{sortKey: key.sortKey - 1, id: maxID}
}
