package tasklist

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
)

// StickyRegistry manages in-memory sticky tasklists for the worker-side
// PollForExternalEvents and DeliverExternalEvents APIs.
//
// Sticky tasklists differ from regular tasklists in three ways:
//  1. Per-worker, not per-named-resource: the tasklist name is the
//     WorkerID
//  2. In-memory only
//  3. Sync-match only: DeliverExternalEvents is non-blocking; if the
//     worker isn't currently polling, the event is dropped silently
//
// Lifecycle:
//   - GetOrCreateSticky(workerID): lazy-creates an entry on first poll
//     or first deliver.
//   - cleanupLoop: periodic sweep of idle entries (no poll AND no
//     deliver in the past N minutes) to prevent unbounded map growth
//     in long-running matching processes that have seen many short-lived
//     workers.
//
// The cleanup loop is a memory-pressure safety net only — entries
// themselves are tiny (one channel + last-activity timestamp), but a
// pathological workload (e.g. CI generating fresh worker IDs constantly)
// could otherwise leak.
type StickyRegistry struct {
	cfg    config.MatchingServiceConfig
	logger log.Logger

	mu      sync.RWMutex
	entries map[string]*stickyEntry // key: workerID

	stopCh      chan struct{}
	cleanupDone chan struct{}
	stopped     atomic.Bool
}

// stickyEntry is the per-worker in-memory rendezvous point. The
// deliverCh is unbuffered: DeliverExternalEvents tries to send
// non-blockingly; PollForExternalEvents blocks on receive.
type stickyEntry struct {
	workerID          string
	deliverCh         chan *pb.ExternalEvent
	lastActiveAtNanos atomic.Int64 // unix nanos
}

// NewStickyRegistry constructs an empty sticky registry. Call Start to
// launch the cleanup loop.
func NewStickyRegistry(cfg config.MatchingServiceConfig, logger log.Logger) *StickyRegistry {
	return &StickyRegistry{
		cfg:         cfg,
		logger:      logger,
		entries:     make(map[string]*stickyEntry),
		stopCh:      make(chan struct{}),
		cleanupDone: make(chan struct{}),
	}
}

// Start launches the cleanup goroutine. Idempotent in practice (registry
// isn't designed to restart).
func (r *StickyRegistry) Start() {
	go r.cleanupLoop()
}

// Stop signals the cleanup loop to exit and waits for it. Idempotent.
// Existing entries' deliverCh is left open — any pollers blocked on a
// receive will be unblocked by their own ctx (PollForExternalEvents
// always uses a long-poll deadline).
func (r *StickyRegistry) Stop() {
	if !r.stopped.CompareAndSwap(false, true) {
		return
	}
	close(r.stopCh)
	<-r.cleanupDone
}

// Deliver attempts a non-blocking sync send of the event to the
// worker's sticky entry. Returns true if a poller accepted the event;
// false if no poller is waiting (event silently dropped — heartbeat
// catch-up is the safety net).
//
// Lazy-creates the entry if it doesn't exist (so the next PollFor call
// can attach). The cleanup loop will sweep unused entries.
func (r *StickyRegistry) Deliver(workerID string, event *pb.ExternalEvent) bool {
	if r.stopped.Load() {
		return false
	}
	entry := r.getOrCreate(workerID)
	entry.lastActiveAtNanos.Store(time.Now().UnixNano())
	select {
	case entry.deliverCh <- event:
		return true
	default:
		return false
	}
}

// Poll blocks until an event arrives at the worker's sticky entry or
// ctx expires. Returns (event, nil) on success, (nil, ctx.Err()) on
// timeout. Lazy-creates the entry on first poll.
func (r *StickyRegistry) Poll(ctx context.Context, workerID string) (*pb.ExternalEvent, error) {
	if r.stopped.Load() {
		return nil, context.Canceled
	}
	entry := r.getOrCreate(workerID)
	entry.lastActiveAtNanos.Store(time.Now().UnixNano())
	select {
	case event := <-entry.deliverCh:
		entry.lastActiveAtNanos.Store(time.Now().UnixNano())
		return event, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.stopCh:
		return nil, context.Canceled
	}
}

// getOrCreate returns the entry for workerID, creating one if absent.
// Uses a double-checked-lock pattern: the common case (entry exists)
// takes only the read lock.
func (r *StickyRegistry) getOrCreate(workerID string) *stickyEntry {
	r.mu.RLock()
	entry, ok := r.entries[workerID]
	r.mu.RUnlock()
	if ok {
		return entry
	}

	r.mu.Lock()
	entry, ok = r.entries[workerID]
	if !ok {
		entry = &stickyEntry{
			workerID:  workerID,
			deliverCh: make(chan *pb.ExternalEvent),
		}
		entry.lastActiveAtNanos.Store(time.Now().UnixNano())
		r.entries[workerID] = entry
	}
	r.mu.Unlock()
	return entry
}

// cleanupLoop runs every cleanup interval, sweeping entries whose
// lastActiveAt is older than StickyIdleTimeout.
func (r *StickyRegistry) cleanupLoop() {
	defer close(r.cleanupDone)

	interval := r.cfg.StickyCleanupInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.sweepIdle()
		}
	}
}

// sweepIdle removes entries that haven't had any activity in the past
// StickyIdleTimeout. Idle == workerID has neither polled nor received
// an event recently.
func (r *StickyRegistry) sweepIdle() {
	idleTimeout := r.cfg.StickyIdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = 30 * time.Minute
	}
	threshold := time.Now().Add(-idleTimeout).UnixNano()

	r.mu.Lock()
	defer r.mu.Unlock()
	swept := 0
	for k, entry := range r.entries {
		if entry.lastActiveAtNanos.Load() < threshold {
			delete(r.entries, k)
			swept++
		}
	}
	if swept > 0 {
		r.logger.Info("StickyRegistry: swept %d idle entries (threshold=%s)", tag.Value(swept))
	}
}
