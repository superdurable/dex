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

package shards

import (
	"context"
	"math"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"

	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/errors"
	"github.com/superdurable/dex/server/internal/log"
	"github.com/superdurable/dex/server/internal/log/tag"
	"github.com/superdurable/dex/server/internal/membership"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// rebalanceDebounceInterval coalesces rapid leave/join events during rolling
// restarts into a single rebalance, reducing shard churn.
const rebalanceDebounceInterval = 3 * time.Second

type ShardManager interface {
	Start(ctx context.Context) error
	Stop()
	// AwaitShardReady waits for the post-claim clock-skew gate.
	// Returns Unavailable if not owned, shutting down, or ctx expires.
	AwaitShardReady(ctx context.Context, shardID int32) errors.CategorizedError
	GetOwnedShards() []int32
	IsLocalShard(shardID int32) bool
	InformShardLost(shardID int32)
	// GetCappedContext caps parentCtx at leaseExpiresAt - LeaseExpiryBuffer, and
	// also cancels when the shard's claim is detached (generation fence).
	GetCappedContext(parentCtx context.Context, shardID int32) (context.Context, context.CancelFunc)
	// ShardContext returns the shard-generation context (cancelled on detach), for
	// task items to carry so a handler is fenced on ownership loss.
	ShardContext(shardID int32) (context.Context, errors.CategorizedError)
	// GetShardOwnerAddress returns the owner's gRPC address, or "" if local.
	GetShardOwnerAddress(shardID int32) string
	// AcquireImmediateTaskSeqLock locks the shard's immediate-seq mutex and
	// returns that shard state. Hold it across NextImmediateSeq AND the DB write,
	// then UnlockImmediate, so commits land in seq-allocation order. Binding to
	// the captured state avoids a re-lookup racing a concurrent re-claim.
	AcquireImmediateTaskSeqLock(shardID int32) (*shardState, errors.CategorizedError)
	// AdvanceTimerReadLevel raises the shard's timer read watermark to readLevel
	// (monotonic). Timer writes floor fire times to this watermark, so no timer is
	// ever committed below where the reader has already read. Call before each read.
	AdvanceTimerReadLevel(shardID int32, readLevel int64) errors.CategorizedError
	// AcquireTimerTaskWriteLock locks the shard's timer-write mutex and returns
	// that shard state. Hold it across FloorFireTime AND the DB write, then
	// UnlockTimer, so a timer cannot be committed below the read watermark.
	AcquireTimerTaskWriteLock(shardID int32) (*shardState, errors.CategorizedError)
}

type shardManagerImpl struct {
	cfg                  *config.ShardConfig
	store                p.ShardStore
	logger               log.Logger
	memberID             string
	taskProcessorManager TaskProcessorsManager
	membership           membership.Membership

	mu          sync.RWMutex
	ownedShards map[int32]*shardState

	// rebalanceMu serializes rebalance phases so release/claim never interleave.
	rebalanceMu sync.Mutex

	ctx        context.Context
	cancel     context.CancelFunc
	shutdownCh chan struct{}
	stopOnce   sync.Once

	rebalanceCh chan struct{}
	loopWG      sync.WaitGroup
}

type shardState struct {
	shardID         int32
	version         int64
	rangeID         int32
	initialMetadata p.ShardMetadata
	leaseExpiresAt  time.Time

	immediateLocalSeq int32
	immediateMu       sync.Mutex

	// timerMu guards timerMaxReadSortKey and serializes timer writes against the
	// reader advancing the watermark, so no timer is committed below where the
	// reader has already read (the fire-time skip guard).
	timerMu             sync.Mutex
	timerMaxReadSortKey int64

	readyCh   chan struct{}
	readyOnce sync.Once

	// genCtx is the shard-generation context, cancelled by renewCancel on any
	// detach (lost/released). Capped contexts and in-flight handlers derive from
	// it, so ownership loss fences all work for this claim immediately.
	genCtx      context.Context
	renewCancel context.CancelFunc

	startMu               sync.Mutex
	taskProcessorsStarted bool
	stopped               bool
}

func NewShardManager(
	cfg *config.ShardConfig,
	store p.ShardStore,
	logger log.Logger,
	memberID string,
	processorManager TaskProcessorsManager,
	internalAddress string,
	onAddressRemoved func(addr string),
) ShardManager {
	if cfg == nil {
		panic("ShardConfig must not be nil")
	}
	if store == nil {
		panic("ShardStore must not be nil")
	}
	if logger == nil {
		panic("logger must not be nil")
	}
	if memberID == "" {
		panic("memberID must not be empty")
	}
	if processorManager == nil {
		panic("processorManager must not be nil")
	}
	if cfg.TotalShards <= 0 {
		panic("TotalShards must be > 0")
	}
	if cfg.LeaseDuration <= 0 {
		panic("LeaseDuration must be > 0")
	}
	if cfg.LeaseRenewInterval <= 0 {
		panic("LeaseRenewInterval must be > 0")
	}

	ctx, cancel := context.WithCancel(context.Background())
	m := &shardManagerImpl{
		cfg:                  cfg,
		store:                store,
		logger:               logger,
		memberID:             memberID,
		taskProcessorManager: processorManager,
		ownedShards:          make(map[int32]*shardState),
		ctx:                  ctx,
		cancel:               cancel,
		shutdownCh:           make(chan struct{}),
		rebalanceCh:          make(chan struct{}, 1),
	}
	m.membership = membership.NewMembership(
		&cfg.Membership,
		logger,
		memberID,
		internalAddress,
		m.triggerRebalance,
		onAddressRemoved,
	)
	return m
}

func (m *shardManagerImpl) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.membership.Start(); err != nil {
		return err
	}

	m.loopWG.Add(1)
	go m.rebalanceLoop()

	m.rebalanceShards()
	m.logger.Info("shard manager started", tag.NodeName(m.memberID))
	return nil
}

func (m *shardManagerImpl) Stop() {
	m.stopOnce.Do(func() {
		close(m.shutdownCh)
		m.loopWG.Wait()

		m.rebalanceMu.Lock()
		defer m.rebalanceMu.Unlock()

		entries := m.stopAllOwned()
		if m.cfg.ShutdownGracefulPeriod > 0 {
			time.Sleep(m.cfg.ShutdownGracefulPeriod)
		}
		if len(entries) > 0 {
			relCtx, relCancel := m.releaseContext()
			if err := m.store.BatchReleaseShards(relCtx, m.memberID, entries); err != nil {
				m.logger.Warn("batch release shards on shutdown failed", tag.Error(err))
			}
			relCancel()
		}

		m.membership.Stop()
		m.cancel()
		m.logger.Info("shard manager stopped", tag.NodeName(m.memberID))
	})
}

func (m *shardManagerImpl) GetOwnedShards() []int32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]int32, 0, len(m.ownedShards))
	for shardID := range m.ownedShards {
		out = append(out, shardID)
	}
	return out
}

func (m *shardManagerImpl) IsLocalShard(shardID int32) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.ownedShards[shardID]
	return ok
}

func (m *shardManagerImpl) GetShardOwnerAddress(shardID int32) string {
	if m.IsLocalShard(shardID) {
		return ""
	}
	ownerID := m.membership.GetMemberIDForKey(strconv.Itoa(int(shardID)))
	if ownerID == "" || ownerID == m.memberID {
		return ""
	}
	return m.membership.GetGrpcAddressForMember(ownerID)
}

func (m *shardManagerImpl) GetCappedContext(parentCtx context.Context, shardID int32) (context.Context, context.CancelFunc) {
	m.mu.RLock()
	state, ok := m.ownedShards[shardID]
	var (
		leaseExpiresAt time.Time
		genCtx         context.Context
	)
	if ok {
		leaseExpiresAt = state.leaseExpiresAt
		genCtx = state.genCtx
	}
	m.mu.RUnlock()

	// Not owned (or lease not yet set): fail fast so an operation cannot run
	// against a shard we do not own. Shutdown cleanup that must run post-detach
	// uses its own bounded context instead of this.
	if !ok || leaseExpiresAt.IsZero() {
		ctx, cancel := context.WithCancel(parentCtx)
		cancel()
		return ctx, cancel
	}

	deadline := leaseExpiresAt.Add(-m.cfg.LeaseExpiryBuffer)
	if deadline.Before(time.Now()) {
		ctx, cancel := context.WithCancel(parentCtx)
		cancel()
		return ctx, cancel
	}

	ctx, cancel := context.WithDeadline(parentCtx, deadline)
	// Fence to the shard generation: cancel the moment this claim is detached
	// (lost/released), not merely at the lease deadline.
	stop := context.AfterFunc(genCtx, cancel)
	return ctx, func() { stop(); cancel() }
}

// ShardContext returns the shard's generation context, cancelled when this claim
// is detached. Task items carry it so a handler is fenced on ownership loss.
func (m *shardManagerImpl) ShardContext(shardID int32) (context.Context, errors.CategorizedError) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.ownedShards[shardID]
	if !ok {
		return nil, errors.NewUnavailableError("shard is not owned locally", nil)
	}
	return state.genCtx, nil
}

func (m *shardManagerImpl) AwaitShardReady(ctx context.Context, shardID int32) errors.CategorizedError {
	if m.isShuttingDown() {
		return errors.NewUnavailableError("shard manager is shutting down", nil)
	}

	m.mu.RLock()
	state, ok := m.ownedShards[shardID]
	m.mu.RUnlock()
	if !ok {
		return errors.NewUnavailableError("shard is not owned locally", nil)
	}

	select {
	case <-state.readyCh:
		if m.isShuttingDown() {
			return errors.NewUnavailableError("shard manager is shutting down", nil)
		}
		m.mu.RLock()
		current, ok := m.ownedShards[shardID]
		m.mu.RUnlock()
		// readyCh also closes on stop; require the SAME state instance so a
		// released-and-re-claimed shard (new state, skew gate not yet open) is
		// not reported ready. The caller retries and awaits the new state.
		if !ok || current != state {
			return errors.NewUnavailableError("shard is not owned locally", nil)
		}
		return nil
	case <-ctx.Done():
		return errors.NewUnavailableError("timed out waiting for shard ready", ctx.Err())
	case <-m.shutdownCh:
		return errors.NewUnavailableError("shard manager is shutting down", nil)
	}
}

func (m *shardManagerImpl) AcquireImmediateTaskSeqLock(shardID int32) (*shardState, errors.CategorizedError) {
	state, err := m.requireOwnedShard(shardID)
	if err != nil {
		return nil, err
	}
	state.immediateMu.Lock()
	return state, nil
}

// NextImmediateSeq returns TaskSeq = (RangeID << 32) | LocalSeq. Call under
// immediateMu (via AcquireImmediateTaskSeqLock). Panics on LocalSeq overflow.
func (s *shardState) NextImmediateSeq() int64 {
	return nextTaskSeq(&s.immediateLocalSeq, s.rangeID, "immediate")
}

func (s *shardState) UnlockImmediate() {
	s.immediateMu.Unlock()
}

func (m *shardManagerImpl) AdvanceTimerReadLevel(shardID int32, readLevel int64) errors.CategorizedError {
	state, err := m.requireOwnedShard(shardID)
	if err != nil {
		return err
	}
	state.timerMu.Lock()
	if readLevel > state.timerMaxReadSortKey {
		state.timerMaxReadSortKey = readLevel
	}
	state.timerMu.Unlock()
	return nil
}

func (m *shardManagerImpl) AcquireTimerTaskWriteLock(shardID int32) (*shardState, errors.CategorizedError) {
	state, err := m.requireOwnedShard(shardID)
	if err != nil {
		return nil, err
	}
	state.timerMu.Lock()
	return state, nil
}

// FloorFireTime returns sortKey, or readWatermark+1 if sortKey is at or below
// the watermark. Call under timerMu (via AcquireTimerTaskWriteLock).
func (s *shardState) FloorFireTime(sortKey int64) int64 {
	if sortKey <= s.timerMaxReadSortKey {
		return s.timerMaxReadSortKey + 1
	}
	return sortKey
}

func (s *shardState) UnlockTimer() {
	s.timerMu.Unlock()
}

func (m *shardManagerImpl) InformShardLost(shardID int32) {
	state := m.detachOwnedShard(shardID)
	if state == nil {
		return
	}

	state.renewCancel()
	// StopShard waits (bounded) for in-flight completions before returning.
	state.stopTaskProcessors(m.taskProcessorManager, true)

	// No extra graceful pause: lease is already lost. StopShard's drain is the
	// bound; release promptly so another member can reclaim.
	relCtx, relCancel := m.releaseContext()
	if err := m.store.ReleaseShard(relCtx, shardID, m.memberID, state.version); err != nil {
		m.logger.Warn("best-effort release after shard lost failed",
			tag.ShardId(shardID), tag.Error(err))
	}
	relCancel()

	m.logger.Warn("shard ownership lost", tag.ShardId(shardID), tag.NodeName(m.memberID))
	m.triggerRebalance()
}

func (m *shardManagerImpl) triggerRebalance() {
	select {
	case m.rebalanceCh <- struct{}{}:
	default:
	}
}

func (m *shardManagerImpl) rebalanceLoop() {
	defer m.loopWG.Done()

	var debounceTimer *time.Timer
	var debounceC <-chan time.Time

	for {
		select {
		case <-m.shutdownCh:
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return
		case <-m.rebalanceCh:
			if debounceTimer == nil {
				debounceTimer = time.NewTimer(rebalanceDebounceInterval)
				debounceC = debounceTimer.C
			} else {
				if !debounceTimer.Stop() {
					select {
					case <-debounceTimer.C:
					default:
					}
				}
				debounceTimer.Reset(rebalanceDebounceInterval)
				debounceC = debounceTimer.C
			}
		case <-debounceC:
			debounceTimer = nil
			debounceC = nil
			m.rebalanceShards()
		}
	}
}

func (m *shardManagerImpl) rebalanceShards() {
	if m.isShuttingDown() {
		return
	}

	m.rebalanceMu.Lock()
	defer m.rebalanceMu.Unlock()

	if m.isShuttingDown() {
		return
	}

	desired := m.membership.GetShardsForMember(m.memberID, m.cfg.TotalShards)
	desiredSet := make(map[int32]struct{}, len(desired))
	for _, shardID := range desired {
		desiredSet[shardID] = struct{}{}
	}

	toRelease := m.shardsToRelease(desiredSet)
	if len(toRelease) > 0 {
		m.releaseShards(toRelease)
	}

	claimFailed := false
	for _, shardID := range desired {
		if m.IsLocalShard(shardID) {
			continue
		}
		if m.isShuttingDown() {
			return
		}
		if err := m.claimShard(shardID); err != nil {
			claimFailed = true
			m.logger.Info("shard claim deferred",
				tag.ShardId(shardID), tag.Error(err))
		}
	}

	if claimFailed && !m.isShuttingDown() {
		m.triggerRebalance()
	}
}

func (m *shardManagerImpl) shardsToRelease(desiredSet map[int32]struct{}) []*shardState {
	m.mu.Lock()
	defer m.mu.Unlock()

	var toRelease []*shardState
	for shardID, state := range m.ownedShards {
		if _, keep := desiredSet[shardID]; keep {
			continue
		}
		delete(m.ownedShards, shardID)
		toRelease = append(toRelease, state)
	}
	return toRelease
}

func (m *shardManagerImpl) releaseShards(states []*shardState) {
	entries := make([]p.ShardReleaseEntry, 0, len(states))
	for _, state := range states {
		state.renewCancel()
		// StopShard waits for in-flight completions (bounded) before returning.
		state.stopTaskProcessors(m.taskProcessorManager, false)
		entries = append(entries, p.ShardReleaseEntry{
			ShardID:         state.shardID,
			ExpectedVersion: state.version,
		})
		m.logger.Info("releasing shard", tag.ShardId(state.shardID))
	}

	// Extra pause after drain so any lease-capped store op started before detach
	// can finish before the shard becomes claimable.
	m.gracefulPause()

	relCtx, relCancel := m.releaseContext()
	if err := m.store.BatchReleaseShards(relCtx, m.memberID, entries); err != nil {
		m.logger.Warn("batch release shards failed", tag.Error(err))
	}
	relCancel()
}

// releaseContext bounds a best-effort shard-release DB call so shutdown cannot
// block on a hung store. The store also caps internally; this is the outer
// budget in case a store impl does not. Background (store-capped only) when
// ShutdownGracefulPeriod is disabled, so the release is never instantly cancelled.
func (m *shardManagerImpl) releaseContext() (context.Context, context.CancelFunc) {
	if m.cfg.ShutdownGracefulPeriod <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), m.cfg.ShutdownGracefulPeriod)
}

// gracefulPause waits ShutdownGracefulPeriod for in-flight work to drain, but
// returns early once shutdown begins so Stop is not delayed.
func (m *shardManagerImpl) gracefulPause() {
	if m.cfg.ShutdownGracefulPeriod <= 0 {
		return
	}
	timer := time.NewTimer(m.cfg.ShutdownGracefulPeriod)
	defer timer.Stop()
	select {
	case <-m.shutdownCh:
	case <-timer.C:
	}
}

// claimShard claims shardID and publishes its state. It self-acquires m.mu, so
// the caller must NOT hold it. Spawned renewal/skew goroutines are tracked in
// loopWG so Stop waits for them.
func (m *shardManagerImpl) claimShard(shardID int32) errors.CategorizedError {
	shard, err := m.claimShardWithRetry(shardID)
	if err != nil {
		return err
	}

	renewCtx, renewCancel := context.WithCancel(m.ctx)
	state := &shardState{
		shardID:             shardID,
		version:             shard.Version,
		rangeID:             shard.Metadata.RangeID,
		initialMetadata:     shard.Metadata,
		leaseExpiresAt:      shard.LeaseExpiresAt,
		timerMaxReadSortKey: shard.Metadata.TimerTaskCommittedSortKey,
		readyCh:             make(chan struct{}),
		genCtx:              renewCtx,
		renewCancel:         renewCancel,
	}

	m.mu.Lock()
	m.ownedShards[shardID] = state
	m.mu.Unlock()

	m.loopWG.Add(1)
	go m.leaseRenewalLoop(renewCtx, state)

	readyAt := time.Unix(0, shard.Metadata.TimerTaskCommittedSortKey)
	if readyAt.After(time.Now()) {
		m.logger.Info("delaying shard taskProcessors for clock-skew gate",
			tag.ShardId(shardID))
		m.loopWG.Add(1)
		go m.startTaskProcessorsAfterSkewGate(renewCtx, state, readyAt)
	} else {
		state.startTaskProcessors(m.taskProcessorManager)
	}

	m.logger.Info("claimed shard",
		tag.ShardId(shardID),
		tag.NodeName(m.memberID))
	return nil
}

func (m *shardManagerImpl) claimShardWithRetry(shardID int32) (*p.Shard, errors.CategorizedError) {
	maxAttempts := m.cfg.Membership.OwnershipOpsMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	var lastErr errors.CategorizedError
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if m.isShuttingDown() {
			return nil, errors.NewUnavailableError("shard manager is shutting down", nil)
		}

		shard, err := m.store.ClaimShard(m.ctx, shardID, m.memberID, m.cfg.LeaseDuration)
		if err == nil {
			return shard, nil
		}
		lastErr = err

		// Still held by another member, or CAS lost — do not burn retries.
		if err.IsConflictError() || err.IsCASError() {
			return nil, err
		}
		if !err.IsRetriableExcludingCASError() || attempt == maxAttempts {
			return nil, err
		}

		sleep := withJitter(m.cfg.Membership.ClaimRetryInterval, m.cfg.Membership.ClaimRetryIntervalJitter)
		if sleep <= 0 {
			sleep = time.Second
		}
		timer := time.NewTimer(sleep)
		select {
		case <-m.shutdownCh:
			timer.Stop()
			return nil, errors.NewUnavailableError("shard manager is shutting down", nil)
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func (m *shardManagerImpl) startTaskProcessorsAfterSkewGate(ctx context.Context, state *shardState, readyAt time.Time) {
	defer m.loopWG.Done()

	timer := time.NewTimer(time.Until(readyAt))
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return
	case <-m.shutdownCh:
		return
	case <-timer.C:
	}

	m.mu.RLock()
	current, ok := m.ownedShards[state.shardID]
	m.mu.RUnlock()
	if !ok || current != state {
		return
	}
	state.startTaskProcessors(m.taskProcessorManager)
}

func (m *shardManagerImpl) leaseRenewalLoop(ctx context.Context, state *shardState) {
	defer m.loopWG.Done()

	for {
		sleep := withJitter(m.cfg.LeaseRenewInterval, m.cfg.LeaseRenewJitter)
		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-m.shutdownCh:
			timer.Stop()
			return
		case <-timer.C:
		}

		if err := m.renewLease(ctx, state); err != nil {
			m.logger.Warn("shard lease renewal failed",
				tag.ShardId(state.shardID), tag.Error(err))
			m.InformShardLost(state.shardID)
			return
		}
	}
}

// renewLease retries a transient renewal failure with jittered backoff until the
// local lease is about to expire, instead of burning a small attempt budget in
// milliseconds. Only a CAS/version mismatch is treated as a definitive fence.
func (m *shardManagerImpl) renewLease(ctx context.Context, state *shardState) errors.CategorizedError {
	metadata := m.taskProcessorManager.GetShardMetadata(state.shardID)

	m.mu.RLock()
	leaseDeadline := state.leaseExpiresAt.Add(-m.cfg.LeaseExpiryBuffer)
	m.mu.RUnlock()

	var lastErr errors.CategorizedError
	for {
		if ctx.Err() != nil {
			return errors.NewUnavailableError("renew canceled", ctx.Err())
		}

		leaseExpiresAt, err := m.store.RenewShardLease(
			ctx, state.shardID, m.memberID, state.version, m.cfg.LeaseDuration, metadata)
		if err == nil {
			m.mu.Lock()
			if current, ok := m.ownedShards[state.shardID]; ok && current == state {
				state.leaseExpiresAt = leaseExpiresAt
			}
			m.mu.Unlock()
			return nil
		}
		lastErr = err
		// CAS/version mismatch: another owner fenced us — give up immediately.
		if err.IsCASError() || err.IsConflictError() || !err.IsRetriableExcludingCASError() {
			return err
		}
		// Transient: keep retrying with backoff while the lease still has room.
		backoff := withJitter(m.cfg.LeaseRenewInterval, m.cfg.LeaseRenewJitter)
		if time.Now().Add(backoff).After(leaseDeadline) {
			return lastErr
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.NewUnavailableError("renew canceled", ctx.Err())
		case <-m.shutdownCh:
			timer.Stop()
			return errors.NewUnavailableError("shard manager is shutting down", nil)
		case <-timer.C:
		}
	}
}

// stopAllOwned detaches and stops every owned shard, returning release entries.
// It self-acquires m.mu, so the caller must NOT hold it.
func (m *shardManagerImpl) stopAllOwned() []p.ShardReleaseEntry {
	m.mu.Lock()
	states := make([]*shardState, 0, len(m.ownedShards))
	for shardID, state := range m.ownedShards {
		delete(m.ownedShards, shardID)
		states = append(states, state)
	}
	m.mu.Unlock()

	entries := make([]p.ShardReleaseEntry, 0, len(states))
	for _, state := range states {
		state.renewCancel()
		state.stopTaskProcessors(m.taskProcessorManager, false)
		entries = append(entries, p.ShardReleaseEntry{
			ShardID:         state.shardID,
			ExpectedVersion: state.version,
		})
	}
	return entries
}

func (m *shardManagerImpl) detachOwnedShard(shardID int32) *shardState {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.ownedShards[shardID]
	if !ok {
		return nil
	}
	delete(m.ownedShards, shardID)
	return state
}

func (m *shardManagerImpl) requireOwnedShard(shardID int32) (*shardState, errors.CategorizedError) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.ownedShards[shardID]
	if !ok {
		return nil, errors.NewUnavailableError("shard is not owned locally", nil)
	}
	return state, nil
}

func (m *shardManagerImpl) isShuttingDown() bool {
	select {
	case <-m.shutdownCh:
		return true
	default:
		return false
	}
}

func (s *shardState) startTaskProcessors(processorsManager TaskProcessorsManager) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.stopped || s.taskProcessorsStarted {
		return
	}
	processorsManager.StartShard(s.shardID, s.initialMetadata)
	s.taskProcessorsStarted = true
	s.markReady()
}

// stopTaskProcessors stops the shard's processors. forced=true (ownership lost)
// skips unfenced store cleanup; forced=false (voluntary release, lease still
// held) lets the deleters flush.
func (s *shardState) stopTaskProcessors(processorsManager TaskProcessorsManager, forced bool) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.stopped = true
	if s.taskProcessorsStarted {
		processorsManager.StopShard(s.shardID, forced)
		s.taskProcessorsStarted = false
	}
	// Unblock AwaitShardReady; callers re-check ownership after wake.
	s.markReady()
}

func (s *shardState) markReady() {
	s.readyOnce.Do(func() {
		close(s.readyCh)
	})
}

func nextTaskSeq(localSeq *int32, rangeID int32, kind string) int64 {
	if *localSeq == math.MaxInt32 {
		panic(kind + " LocalSeq overflow; restart required")
	}
	seq := *localSeq
	*localSeq++
	return int64(rangeID)<<32 | int64(uint32(seq))
}

func withJitter(base, jitter time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	if jitter <= 0 {
		return base
	}
	return base + time.Duration(rand.Int64N(int64(jitter)+1))
}
