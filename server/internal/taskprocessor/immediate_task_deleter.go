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

	"github.com/google/btree"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/log"
	"github.com/superdurable/dex/server/internal/log/tag"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shards"
)

// ImmediateTaskDeleter tracks in-flight immediate tasks;
// watermark is inclusive RangeDelete upper bound.
type ImmediateTaskDeleter interface {
	Start(ctx context.Context)
	Stop()
	DoneCh() chan<- TaskCompletion
	InsertPending(seq int64)
	GetWatermark() int64
}

type immediateTaskDeleterImpl struct {
	cfg     *config.TaskProcessorConfig
	store   p.RunStore
	sm      shards.ShardManager
	shardID int32
	logger  log.Logger

	doneCh chan TaskCompletion

	mu           sync.Mutex
	pending      *btree.BTreeG[int64]
	watermark    int64
	maxCompleted int64

	cancel       context.CancelFunc
	wg           sync.WaitGroup
	shuttingDown atomic.Bool
}

var _ ImmediateTaskDeleter = (*immediateTaskDeleterImpl)(nil)

// opCtx returns the context for a store op. During normal operation it caps at
// the shard lease. During shutdown the shard is already detached (GetCappedContext
// would fail fast), so it uses the caller's already-bounded context directly.
func (d *immediateTaskDeleterImpl) opCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if d.shuttingDown.Load() {
		return context.WithCancel(ctx)
	}
	return d.sm.GetCappedContext(ctx, d.shardID)
}

// NewImmediateTaskDeleter starts from the shard's committed inclusive watermark.
func NewImmediateTaskDeleter(
	cfg *config.TaskProcessorConfig,
	store p.RunStore,
	sm shards.ShardManager,
	shardID int32,
	initialSeq int64,
	logger log.Logger,
) ImmediateTaskDeleter {
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
	if cfg.ImmediateDeleteInterval <= 0 {
		panic("TaskProcessorConfig.ImmediateDeleteInterval must be > 0")
	}
	if cfg.ShutdownGracePeriod <= 0 {
		panic("TaskProcessorConfig.ShutdownGracePeriod must be > 0")
	}

	return &immediateTaskDeleterImpl{
		cfg:          cfg,
		store:        store,
		sm:           sm,
		shardID:      shardID,
		logger:       logger,
		doneCh:       make(chan TaskCompletion, cfg.NumWorkers*2),
		pending:      btree.NewG(32, func(a, b int64) bool { return a < b }),
		watermark:    initialSeq,
		maxCompleted: initialSeq,
	}
}

func (d *immediateTaskDeleterImpl) Start(ctx context.Context) {
	if d.cancel != nil {
		panic("ImmediateTaskDeleter already started")
	}
	ctx, d.cancel = context.WithCancel(ctx)
	d.wg.Add(1)
	go d.run(ctx)
}

func (d *immediateTaskDeleterImpl) Stop() {
	if d.cancel == nil {
		return
	}
	d.cancel()
	d.wg.Wait()
	d.cancel = nil
	d.shuttingDown.Store(true)

	// Bound the final drain so a hung store call cannot block shutdown forever.
	// The shard is already detached, so opCtx uses this bounded ctx directly.
	ctx, cancel := context.WithTimeout(context.Background(), d.cfg.ShutdownGracePeriod)
	defer cancel()
	d.drainDone(ctx)
}

func (d *immediateTaskDeleterImpl) run(ctx context.Context) {
	defer d.wg.Done()

	timer := time.NewTimer(withJitter(d.cfg.ImmediateDeleteInterval, d.cfg.ImmediateDeleteIntervalJitter))
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
			timer.Reset(withJitter(d.cfg.ImmediateDeleteInterval, d.cfg.ImmediateDeleteIntervalJitter))
		}
	}
}

func (d *immediateTaskDeleterImpl) drainDone(ctx context.Context) {
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

func (d *immediateTaskDeleterImpl) onComplete(completion TaskCompletion) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.pending.Delete(completion.SortKey); !ok {
		d.logger.Error("immediate task completion for unknown pending task",
			tag.ShardId(d.shardID),
		)
		return
	}
	if completion.SortKey > d.maxCompleted {
		d.maxCompleted = completion.SortKey
	}
}

func (d *immediateTaskDeleterImpl) tryAdvance(ctx context.Context) {
	for {
		d.mu.Lock()
		candidate := d.computeWatermarkLocked()
		if candidate <= d.watermark {
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()

		// opCtx caps at the shard lease during normal run (bounded ctx on shutdown).
		capped, cancel := d.opCtx(ctx)
		err := d.store.RangeDeleteImmediateTasks(capped, d.shardID, candidate)
		cancel()
		if err != nil {
			d.logger.Error("range delete immediate tasks failed",
				tag.ShardId(d.shardID),
				tag.Error(err),
			)
			return
		}

		d.mu.Lock()
		if minSeq, ok := d.pending.Min(); ok && minSeq <= candidate {
			d.mu.Unlock()
			d.logger.Error("pending immediate task at or below delete watermark",
				tag.ShardId(d.shardID),
			)
			return
		}
		if candidate > d.watermark {
			d.watermark = candidate
		}
		again := d.computeWatermarkLocked()
		more := again > candidate
		d.mu.Unlock()
		if !more {
			return
		}
	}
}

// computeWatermarkLocked returns the inclusive delete upper bound.
// Pending non-empty: min(pending)-1. Empty: max completed seq.
func (d *immediateTaskDeleterImpl) computeWatermarkLocked() int64 {
	if minSeq, ok := d.pending.Min(); ok {
		return minSeq - 1
	}
	return d.maxCompleted
}

func (d *immediateTaskDeleterImpl) DoneCh() chan<- TaskCompletion {
	return d.doneCh
}

// InsertPending records a task before the reader hands it to the executor.
func (d *immediateTaskDeleterImpl) InsertPending(seq int64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if seq <= d.watermark {
		panic("InsertPending seq must be above delete watermark")
	}
	if _, existed := d.pending.ReplaceOrInsert(seq); existed {
		panic("InsertPending duplicate immediate task seq")
	}
}

func (d *immediateTaskDeleterImpl) GetWatermark() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.watermark
}
