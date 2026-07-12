package taskprocessor

import (
	"context"
	"sync"

	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/historynotify"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shardmanager"
)

type shardTaskDeleters struct {
	immediate *ImmediateBatchDeleter
	timer     *TimerBatchDeleter
	opsFIFO   *OpsBatchDeleter
}

// ShardTaskProcessorFactory creates per-shard batch readers when a shard is claimed.
// It implements shardmanager.ComponentFactory.
//
// Construction order: NewShardTaskProcessorFactory -> SetWorkerPool (after WorkerPool is
// created, which depends on ShardManager, which depends on this factory).
//
// historyStore + visibilityStore are optional: when nil the OpsBatchReader is
// not started for new shards. This is the path used by integration tests
// that don't want to exercise the OpsFIFO loop, and by deployments where the
// downstream stores are unconfigured (the OpsFIFOTaskRow rows still accumulate
// on disk and a future deploy with the stores wired in will drain them).
type ShardTaskProcessorFactory struct {
	cfg               config.TaskProcessorConfig
	runStore          p.RunStore
	historyStore      p.HistoryStore
	visibilityStore   p.VisibilityStore
	historyNotifier   historynotify.NotifierManager
	wp                *WorkerPool
	localTaskNotifier *LocalTaskNotifier
	logger            log.Logger

	mu       sync.RWMutex
	deleters map[int32]*shardTaskDeleters
}

func NewShardTaskProcessorFactory(
	cfg config.TaskProcessorConfig,
	runStore p.RunStore,
	historyStore p.HistoryStore,
	visibilityStore p.VisibilityStore,
	historyNotifier historynotify.NotifierManager,
	taskNotifier *LocalTaskNotifier,
	logger log.Logger,
) *ShardTaskProcessorFactory {
	return &ShardTaskProcessorFactory{
		cfg:               cfg,
		runStore:          runStore,
		historyStore:      historyStore,
		visibilityStore:   visibilityStore,
		historyNotifier:   historyNotifier,
		localTaskNotifier: taskNotifier,
		logger:            logger,
		deleters:          make(map[int32]*shardTaskDeleters),
	}
}

// SetWorkerPool must be called before ShardManager.Start(). Breaks the
// circular dependency: Factory -> WorkerPool -> TaskHandler -> Engine -> ShardManager -> Factory.
func (f *ShardTaskProcessorFactory) SetWorkerPool(wp *WorkerPool) {
	f.wp = wp
}

// GetMetadataForShard returns the current watermark offsets for a shard,
// used as the MetadataCallback by the shard manager during lease renewal.
// OpsFIFOTaskCommittedSeq is sourced from the OpsBatchDeleter when the
// OpsFIFO loop is wired (history + visibility stores configured); otherwise
// it stays at the value the shard was claimed with.
func (f *ShardTaskProcessorFactory) GetMetadataForShard(shardID int32) *p.ShardMetadata {
	f.mu.RLock()
	d, ok := f.deleters[shardID]
	f.mu.RUnlock()
	if !ok {
		return nil
	}
	timerSK, timerID := d.timer.GetWatermark()
	md := &p.ShardMetadata{
		ImmediateTaskCommittedSeq: d.immediate.GetWatermark(),
		TimerTaskCommittedSortKey: timerSK,
		TimerTaskCommittedID:      timerID,
	}
	if d.opsFIFO != nil {
		md.OpsFIFOTaskCommittedSeq = d.opsFIFO.GetWatermark()
	}
	return md
}

// StartComponents is called by ShardManager when a shard is successfully claimed.
// It creates notification channels and launches batch readers and deleters for the shard.
func (f *ShardTaskProcessorFactory) StartComponents(shardID int32, handle *shardmanager.ShardHandle, sm shardmanager.ShardManager) error {
	ch := NewShardTaskNotifier()

	f.localTaskNotifier.Register(shardID, ch)

	metadata := handle.Metadata()

	immediateDeleter := NewImmediateBatchDeleter(
		shardID, f.cfg, f.runStore, sm, f.logger,
		handle.ShutdownCh(), metadata.ImmediateTaskCommittedSeq,
	)
	timerDeleter := NewTimerBatchDeleter(
		shardID, f.cfg, f.runStore, sm, f.logger,
		handle.ShutdownCh(), metadata.TimerTaskCommittedSortKey, metadata.TimerTaskCommittedID,
	)
	var opsFIFODeleter *OpsBatchDeleter
	if f.opsFIFOWired() {
		opsFIFODeleter = NewOpsBatchDeleter(
			shardID, f.cfg, f.runStore, sm, f.logger,
			handle.ShutdownCh(), metadata.OpsFIFOTaskCommittedSeq,
		)
	}

	f.mu.Lock()
	f.deleters[shardID] = &shardTaskDeleters{immediate: immediateDeleter, timer: timerDeleter, opsFIFO: opsFIFODeleter}
	f.mu.Unlock()

	immediateReader := NewImmediateBatchReader(
		shardID, f.cfg, f.runStore, f.wp, immediateDeleter,
		handle.ShutdownCh(), sm, f.logger, metadata.ImmediateTaskCommittedSeq, ch.newTaskCh,
	)
	timerReader := NewTimerBatchReader(
		shardID, f.cfg, f.runStore, f.wp, timerDeleter,
		handle.ShutdownCh(), sm, f.logger,
		metadata.TimerTaskCommittedSortKey, metadata.TimerTaskCommittedID, ch,
	)

	ctx := context.Background()
	go immediateReader.Run(ctx)
	go timerReader.Run(ctx)
	go immediateDeleter.Run(ctx)
	go timerDeleter.Run(ctx)
	if opsFIFODeleter != nil {
		opsFIFOReader := NewOpsBatchReader(
			shardID, f.cfg, f.runStore, f.historyStore, f.visibilityStore, f.historyNotifier, opsFIFODeleter,
			sm, f.logger, handle.ShutdownCh(), metadata.OpsFIFOTaskCommittedSeq, ch.OpsFIFOCh(),
		)
		go opsFIFOReader.Run(ctx)
		go opsFIFODeleter.Run(ctx)
	}

	f.logger.Info("Started batch readers for shard", tag.Shard(shardID))
	return nil
}

func (f *ShardTaskProcessorFactory) opsFIFOWired() bool {
	return f.historyStore != nil && f.visibilityStore != nil
}

// StopComponents is called by ShardManager when a shard is released.
func (f *ShardTaskProcessorFactory) StopComponents(shardID int32) {
	f.mu.Lock()
	delete(f.deleters, shardID)
	f.mu.Unlock()

	f.localTaskNotifier.Unregister(shardID)
	f.logger.Info("Stopped batch readers for shard", tag.Shard(shardID))
}
