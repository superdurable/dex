package tasklist

import (
	"context"
	"sync/atomic"

	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// tasklistDB wraps a TasklistStore for a single tasklist partition.
//
// Lifecycle invariant: an instance always represents a SUCCESSFULLY CLAIMED
// tasklist. The constructor takes the rangeID + initial ackLevel returned
// from store.ClaimTasklist, so there is no "unclaimed / partially-initialized"
// state to defend against. If a fence error later invalidates ownership,
// the manager self-evicts and a fresh tasklistDB is constructed for the
// new claim — never re-claimed in place.
//
// Concurrency:
//   - rangeID is set once at construction and never changes; it is a
//     plain field (no atomic, no mutex). Goroutines launched by the
//     manager AFTER tasklistDB construction observe rangeID via
//     happens-before of the goroutine launch.
//   - ackLevel is mutated by the single-writer Pattern: only the
//     task_reader goroutine's persistAckLevelAndGC + Manager.Stop
//     (after reader.Stop has joined) ever call UpdateAckLevel. Plain
//     atomic.Int64 is sufficient — no CompareAndSwap needed.
type tasklistDB struct {
	store p.TasklistStore
	id    *Identifier

	rangeID  int32        // immutable after construction
	ackLevel atomic.Int64 // single-writer, lock-free reads
}

// newTasklistDB constructs a tasklistDB for an already-claimed partition.
// Callers (currently Manager.Start) must invoke store.ClaimTasklist
// first and pass the returned rangeID + initial ackLevel here.
func newTasklistDB(store p.TasklistStore, id *Identifier, rangeID int32, initialAckLevel int64) *tasklistDB {
	db := &tasklistDB{
		store:   store,
		id:      id,
		rangeID: rangeID,
	}
	db.ackLevel.Store(initialAckLevel)
	return db
}

// RangeID returns the immutable range ID for this tasklist.
func (db *tasklistDB) RangeID() int32 { return db.rangeID }

// AckLevel returns the currently-cached ack level.
func (db *tasklistDB) AckLevel() int64 { return db.ackLevel.Load() }

// UpdateAckLevel performs a fenced UpdateTasklistMetadata using the
// (immutable) rangeID. On success, advances the cached ackLevel. On
// fence failure, returns an OwnerVersionMismatchError without touching
// the cached state — caller (manager) should treat as ownership lost.
//
// No-ops if ackLevel <= cached ackLevel: ackLevel must monotonically
// advance, and persisting a stale or equal value is wasted DB IO.
//
// IMPORTANT: this method assumes a single-writer caller. In our design
// only task_reader's periodic persistAckLevelAndGC calls it, plus
// Manager.Stop AFTER reader.Stop has fully exited. If a future change
// introduces concurrent callers, the entire (check + DB write + cache
// update) must be serialized — atomic.Store alone would not prevent
// DB regression.
func (db *tasklistDB) UpdateAckLevel(ctx context.Context, ackLevel int64) errors.CategorizedError {
	if ackLevel <= db.ackLevel.Load() {
		return nil
	}
	if err := db.store.UpdateTasklistMetadata(ctx, db.id.Namespace(), db.id.BaseName(), db.id.Partition(), db.rangeID, ackLevel); err != nil {
		return err
	}
	db.ackLevel.Store(ackLevel)
	return nil
}

// CreateTasks performs a fenced batch INSERT of task rows using the
// (immutable) rangeID. On fence failure, returns an
// OwnerVersionMismatchError.
func (db *tasklistDB) CreateTasks(ctx context.Context, tasks []*p.TasklistTaskRow) errors.CategorizedError {
	return db.store.CreateTasks(ctx, db.id.Namespace(), db.id.BaseName(), db.id.Partition(), db.rangeID, tasks)
}

// GetTasks reads task rows with task_id in (readLevel, maxReadLevel],
// ordered by task_id ASC, limited to batchSize. Not fenced — any owner
// (including a new owner draining old tasks) can read.
func (db *tasklistDB) GetTasks(ctx context.Context, readLevel, maxReadLevel int64, batchSize int) ([]*p.TasklistTaskRow, errors.CategorizedError) {
	return db.store.GetTasks(ctx, db.id.Namespace(), db.id.BaseName(), db.id.Partition(), readLevel, maxReadLevel, batchSize)
}

// DeleteTasksLessThan deletes task rows with task_id <= ackLevel, up to
// limit. Not fenced.
func (db *tasklistDB) DeleteTasksLessThan(ctx context.Context, ackLevel int64, limit int) (int, errors.CategorizedError) {
	return db.store.DeleteTasksLessThan(ctx, db.id.Namespace(), db.id.BaseName(), db.id.Partition(), ackLevel, limit)
}

// DeleteTasksByIDBatch deletes task rows by exact task_id list. Used
// during shutdown for completed-above-watermark cleanup. Not fenced.
func (db *tasklistDB) DeleteTasksByIDBatch(ctx context.Context, taskIDs []int64) errors.CategorizedError {
	return db.store.DeleteTasksByIDBatch(ctx, db.id.Namespace(), db.id.BaseName(), db.id.Partition(), taskIDs)
}
