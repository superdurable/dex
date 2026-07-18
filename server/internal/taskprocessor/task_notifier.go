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
	"sync"
	"sync/atomic"
)

// TaskNotifier wakes per-shard batch readers after engine task writes.
// Process-local only: writes occur on the shard owner.
type TaskNotifier interface {
	// AddShard creates and stores a per-shard notifier for readers.
	AddShard(shardID int32) *perShardNotifier
	RemoveShard(shardID int32)
	NotifyNewImmediateTask(shardID int32)
	NotifyNewTimerTask(shardID int32, fireAtUnixMs int64)
}

type taskNotifierImpl struct {
	mu        sync.RWMutex
	notifiers map[int32]*perShardNotifier
}

var _ TaskNotifier = (*taskNotifierImpl)(nil)

func NewTaskNotifier() TaskNotifier {
	return &taskNotifierImpl{
		notifiers: make(map[int32]*perShardNotifier),
	}
}

// AddShard creates and stores the shard's notifier. Called on shard claim.
func (n *taskNotifierImpl) AddShard(shardID int32) *perShardNotifier {
	notifier := newPerShardNotifier()
	n.mu.Lock()
	n.notifiers[shardID] = notifier
	n.mu.Unlock()
	return notifier
}

// RemoveShard drops the shard's notifier. Called when the shard is released.
func (n *taskNotifierImpl) RemoveShard(shardID int32) {
	n.mu.Lock()
	delete(n.notifiers, shardID)
	n.mu.Unlock()
}

// NotifyNewImmediateTask rings the immediate doorbell. No-op if shard is remote.
func (n *taskNotifierImpl) NotifyNewImmediateTask(shardID int32) {
	notifier := n.get(shardID)
	if notifier == nil {
		return
	}
	select {
	case notifier.newTaskCh <- struct{}{}:
	default:
	}
}

// NotifyNewTimerTask wakes the timer reader and may advance its next wakeup.
func (n *taskNotifierImpl) NotifyNewTimerTask(shardID int32, fireAtUnixMs int64) {
	if fireAtUnixMs <= 0 {
		return
	}
	notifier := n.get(shardID)
	if notifier == nil {
		return
	}
	notifier.notifyTimer(fireAtUnixMs)
}

func (n *taskNotifierImpl) get(shardID int32) *perShardNotifier {
	n.mu.RLock()
	notifier := n.notifiers[shardID]
	n.mu.RUnlock()
	return notifier
}

// perShardNotifier holds capacity-1 doorbells for one shard's batch readers.
// pendingEarliestFireAt lets the timer reader pull nextWakeupTime earlier
// when a sooner timer is written past MinLookAhead.
type perShardNotifier struct {
	newTaskCh             chan struct{}
	newTimerCh            chan struct{}
	pendingEarliestFireAt atomic.Int64
}

func newPerShardNotifier() *perShardNotifier {
	return &perShardNotifier{
		newTaskCh:  make(chan struct{}, 1),
		newTimerCh: make(chan struct{}, 1),
	}
}

// notifyTimer CAS-mins pendingEarliestFireAt, then rings newTimerCh.
func (c *perShardNotifier) notifyTimer(fireAtUnixMs int64) {
	for {
		cur := c.pendingEarliestFireAt.Load()
		if cur != 0 && cur <= fireAtUnixMs {
			return
		}
		if c.pendingEarliestFireAt.CompareAndSwap(cur, fireAtUnixMs) {
			break
		}
	}
	select {
	case c.newTimerCh <- struct{}{}:
	default:
	}
}
