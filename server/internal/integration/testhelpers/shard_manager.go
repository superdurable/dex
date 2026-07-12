package testhelpers

import (
	"context"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/internal/shardmanager"
)

// FakeShardManager is a stub ShardManager for tests that need a fully wired
// engine but don't care about real shard ownership. It claims every shard as
// local, hands out monotonically increasing per-stream task sequences, and
// short-circuits lock helpers. Used by tests that exercise the engine /
// task-processor pipeline without spinning up the full cluster machinery.
type FakeShardManager struct {
	immediateSeq int64
	opsFIFOSeq   int64
}

func (m *FakeShardManager) Start(_ context.Context) error       { return nil }
func (m *FakeShardManager) Stop()                               {}
func (m *FakeShardManager) GetOwnedShards() []int32             { return []int32{0, 1} }
func (m *FakeShardManager) IsLocalShard(_ int32) bool           { return true }
func (m *FakeShardManager) SignalShardLost(_ int32)             {}
func (m *FakeShardManager) GetShardOwnerAddress(_ int32) string { return "" }
func (m *FakeShardManager) GetCappedContext(ctx context.Context, _ int32) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 30*time.Second)
}
func (m *FakeShardManager) AcquireImmediateTaskSeqLock(_ int32) (func(), errors.CategorizedError) {
	return func() {}, nil
}
func (m *FakeShardManager) GetNextImmediateTaskSeq(_ int32) (int64, error) {
	m.immediateSeq++
	return m.immediateSeq, nil
}
func (m *FakeShardManager) AcquireOpsFIFOTaskSeqLock(_ int32) (func(), errors.CategorizedError) {
	return func() {}, nil
}
func (m *FakeShardManager) GetNextOpsFIFOTaskSeq(_ int32) (int64, error) {
	m.opsFIFOSeq++
	return m.opsFIFOSeq, nil
}
func (m *FakeShardManager) GetShardVersion(_ int32) int64                       { return 1 }
func (m *FakeShardManager) SetMetadataCallback(_ shardmanager.MetadataCallback) {}
func (m *FakeShardManager) AwaitShardReady(_ context.Context, _ int32) errors.CategorizedError {
	return nil
}
