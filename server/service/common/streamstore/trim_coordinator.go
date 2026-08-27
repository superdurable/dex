// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package streamstore

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/service/common/log"
	"github.com/superdurable/dex/service/common/log/tag"
)

const (
	trimLeaseTTL       = 5 * time.Second
	trimLeaseRetry     = 100 * time.Millisecond
	trimBatchYieldTime = time.Millisecond
)

type trimCoordinator struct {
	client  *redis.Client
	logger  log.Logger
	ownerID string
	ctx     context.Context
	cancel  context.CancelFunc

	mutex     sync.Mutex
	ready     *sync.Cond
	pending   map[streamScope]*trimState
	closed    bool
	wait      sync.WaitGroup
	stop      chan struct{}
	closeOnce sync.Once
}

type trimState struct {
	targetBytes int64
	generation  uint64
	isRunning   bool
}

type streamScope struct {
	flowType   string
	streamName string
}

type trimTask struct {
	scope      streamScope
	target     int64
	generation uint64
}

func newTrimCoordinator(
	cfg *config.StreamStoreConfig,
	client *redis.Client,
	logger log.Logger,
) *trimCoordinator {
	if cfg == nil || client == nil || logger == nil {
		panic("Trim Coordinator dependencies must not be nil")
	}
	coordinatorCtx, cancelCoordinator := context.WithCancel(context.Background())
	coordinator := &trimCoordinator{
		client:  client,
		logger:  logger,
		ownerID: uuid.NewString(),
		ctx:     coordinatorCtx,
		cancel:  cancelCoordinator,
		pending: make(map[streamScope]*trimState),
		stop:    make(chan struct{}),
	}
	coordinator.ready = sync.NewCond(&coordinator.mutex)
	for workerIndex := 0; workerIndex < cfg.EffectiveTrimWorkers(); workerIndex++ {
		coordinator.wait.Add(1)
		go coordinator.runWorker()
	}
	return coordinator
}

func (c *trimCoordinator) Schedule(flowType string, streamName string, targetBytes int64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return
	}
	scope := streamScope{flowType: flowType, streamName: streamName}
	state := c.pending[scope]
	if state == nil {
		state = &trimState{targetBytes: targetBytes}
		c.pending[scope] = state
	} else {
		if targetBytes < state.targetBytes {
			state.targetBytes = targetBytes
		} else if !state.isRunning {
			return
		}
	}
	state.generation++
	c.ready.Signal()
}

func (c *trimCoordinator) Close() {
	c.closeOnce.Do(func() {
		c.mutex.Lock()
		c.closed = true
		c.ready.Broadcast()
		c.mutex.Unlock()
		close(c.stop)
		c.cancel()
	})
	c.wait.Wait()
}

func (c *trimCoordinator) runWorker() {
	defer c.wait.Done()
	for {
		task, ok := c.nextTask()
		if !ok {
			return
		}
		if err := c.trim(task.scope, task.target); err != nil && !c.isClosed() {
			c.logger.Error("background Stream trim failed", tag.Error(err))
		}
		c.finishTask(task)
	}
}

func (c *trimCoordinator) nextTask() (trimTask, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	for {
		if c.closed {
			return trimTask{}, false
		}
		for scope, state := range c.pending {
			if state.isRunning {
				continue
			}
			state.isRunning = true
			return trimTask{
				scope:      scope,
				target:     state.targetBytes,
				generation: state.generation,
			}, true
		}
		c.ready.Wait()
	}
}

func (c *trimCoordinator) finishTask(task trimTask) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	state := c.pending[task.scope]
	if state == nil {
		return
	}
	if state.generation == task.generation {
		delete(c.pending, task.scope)
		return
	}
	state.isRunning = false
	c.ready.Signal()
}

func (c *trimCoordinator) trim(scope streamScope, targetBytes int64) error {
	keys := streamKeys(scope.flowType, scope.streamName, "")
	leaseOwner := c.ownerID + ":" + uuid.NewString()
	for {
		if c.isClosed() {
			return nil
		}
		acquired, err := c.client.SetNX(c.ctx, keys.lease, leaseOwner, trimLeaseTTL).Result()
		if err != nil {
			return fmt.Errorf("acquire Redis trim lease: %w", err)
		}
		if acquired {
			break
		}
		if !c.waitFor(trimLeaseRetry) {
			return nil
		}
	}
	defer func() {
		if c.isClosed() {
			return
		}
		if err := runReleaseLeaseScript(c.ctx, c.client, releaseLeaseScriptInput{
			leaseKey:   keys.lease,
			leaseOwner: leaseOwner,
		}); err != nil {
			c.logger.Error("release Stream trim lease failed", tag.Error(err))
		}
	}()

	for {
		renewOutput, err := runRenewLeaseScript(
			c.ctx,
			c.client,
			renewLeaseScriptInput{
				leaseKey:   keys.lease,
				leaseOwner: leaseOwner,
				leaseTTL:   trimLeaseTTL,
			},
		)
		if err != nil {
			return err
		}
		if !renewOutput.isRenewed {
			return nil
		}
		trimOutput, err := runTrimScript(c.ctx, c.client, trimScriptInput{
			keys:        keys,
			targetBytes: targetBytes,
			leaseOwner:  leaseOwner,
		})
		if err != nil {
			return err
		}
		if trimOutput.remainingBytes < 0 || trimOutput.remainingBytes <= targetBytes {
			return nil
		}
		if !c.waitFor(trimBatchYieldTime) {
			return nil
		}
	}
}

func (c *trimCoordinator) waitFor(duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return !c.isClosed()
	case <-c.stop:
		return false
	}
}

func (c *trimCoordinator) isClosed() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.closed
}
