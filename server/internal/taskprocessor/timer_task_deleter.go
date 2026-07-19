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
	"time"

	"common-go/ids"

	"github.com/google/btree"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/log"
	"github.com/superdurable/dex/server/internal/log/tag"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shards"
)

// TimerTaskDeleter tracks in-flight timer tasks; watermark is exclusive RangeDelete upper bound.
type TimerTaskDeleter interface {
	Start(ctx context.Context)
	Stop()
	DoneCh() chan<- TaskCompletion
	InsertPending(sortKey int64, id ids.UID)
	GetWatermark() (sortKey int64, id ids.UID)
}

type timerTaskDeleterImpl struct {
	cfg     *config.TaskProcessorConfig
	store   p.RunStore
	sm      shards.ShardManager
	shardID int32
	logger  log.Logger

	doneCh chan TaskCompletion

	mu             sync.Mutex
	pending        *btree.BTreeG[timerTaskKey]
	wmSortKey      int64
	wmID           ids.UID
	completedAbove map[ids.UID]timerTaskKey

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var _ TimerTaskDeleter = (*timerTaskDeleterImpl)(nil)

// NewTimerTaskDeleter starts from the shard's committed exclusive watermark.
func NewTimerTaskDeleter(
	cfg *config.TaskProcessorConfig,
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
	if cfg.ShutdownGracePeriod <= 0 {
		panic("TaskProcessorConfig.ShutdownGracePeriod must be > 0")
	}

	return &timerTaskDeleterImpl{
		cfg:            cfg,
		store:          store,
		sm:             sm,
		shardID:        shardID,
		logger:         logger,
		doneCh:         make(chan TaskCompletion, cfg.NumWorkers*2),
		pending:        btree.NewG(32, timerTaskKeyLess),
		wmSortKey:      initialSortKey,
		wmID:           initialID,
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

func (d *timerTaskDeleterImpl) Stop() {
	if d.cancel == nil {
		return
	}
	d.cancel()
	d.wg.Wait()
	d.cancel = nil

	// Bound the final drain + flush so a hung store call cannot block shutdown
	// forever. The timeout propagates through GetCappedContext to each DB op.
	ctx, cancel := context.WithTimeout(context.Background(), d.cfg.ShutdownGracePeriod)
	defer cancel()
	d.drainDone(ctx)
	d.flushCompletedAbove(ctx)
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
			// Periodic RangeDelete; jitter desynchronizes shards.
			d.tryAdvance(ctx)
			timer.Reset(withJitter(d.cfg.TimerDeleteInterval, d.cfg.TimerDeleteIntervalJitter))
		}
	}
}

func (d *timerTaskDeleterImpl) drainDone(ctx context.Context) {
	for {
		select {
		case completion := <-d.doneCh:
			d.onComplete(completion)
		default:
			d.tryAdvance(ctx)
			return
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
	// Exclusive RangeDelete misses keys >= watermark; keep them for by-ID delete.
	wm := timerTaskKey{sortKey: d.wmSortKey, id: d.wmID}
	if !timerTaskKeyLess(key, wm) {
		d.completedAbove[key.id] = key
	}
}

func (d *timerTaskDeleterImpl) tryAdvance(ctx context.Context) {
	for {
		d.mu.Lock()
		candidate, ok := d.pending.Min()
		if !ok {
			d.mu.Unlock()
			return
		}
		current := timerTaskKey{sortKey: d.wmSortKey, id: d.wmID}
		if !timerTaskKeyLess(current, candidate) {
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()

		// Cap at the shard lease so a delete cannot outlive ownership.
		capped, cancel := d.sm.GetCappedContext(ctx, d.shardID)
		err := d.store.RangeDeleteTimerTasks(capped, d.shardID, candidate.sortKey, candidate.id)
		cancel()
		if err != nil {
			d.logger.Error("range delete timer tasks failed",
				tag.ShardId(d.shardID),
				tag.Error(err),
			)
			return
		}

		d.mu.Lock()
		if minKey, ok := d.pending.Min(); ok && timerTaskKeyLess(minKey, candidate) {
			d.mu.Unlock()
			d.logger.Error("pending timer task below delete watermark",
				tag.ShardId(d.shardID),
			)
			return
		}
		if timerTaskKeyLess(timerTaskKey{sortKey: d.wmSortKey, id: d.wmID}, candidate) {
			d.wmSortKey = candidate.sortKey
			d.wmID = candidate.id
			d.pruneCompletedAboveLocked(candidate)
		}
		again, againOK := d.pending.Min()
		more := againOK && timerTaskKeyLess(candidate, again)
		d.mu.Unlock()
		if !more {
			return
		}
	}
}

// flushCompletedAbove deletes tasks RangeDelete could not cover (>= watermark).
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

	// Page the by-ID deletes so a large completed-above set does not overload
	// the store in a single call.
	for start := 0; start < len(uids); start += batchSize {
		end := start + batchSize
		if end > len(uids) {
			end = len(uids)
		}
		page := uids[start:end]

		// Cap at the shard lease so the cleanup cannot outlive ownership.
		capped, cancel := d.sm.GetCappedContext(ctx, d.shardID)
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

// InsertPending records a task before the reader hands it to the executor.
// In-memory only: the watermark advances and deletes happen on the deleter
// goroutine (onComplete / ticker), never on the reader's goroutine.
func (d *timerTaskDeleterImpl) InsertPending(sortKey int64, id ids.UID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := timerTaskKey{sortKey: sortKey, id: id}
	if !timerTaskKeyLess(timerTaskKey{sortKey: d.wmSortKey, id: d.wmID}, key) {
		panic("InsertPending key must be above delete watermark")
	}
	if _, existed := d.pending.ReplaceOrInsert(key); existed {
		panic("InsertPending duplicate timer task key")
	}
}

func (d *timerTaskDeleterImpl) GetWatermark() (sortKey int64, id ids.UID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.wmSortKey, d.wmID
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
