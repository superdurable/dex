// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package interpreter

import (
	"context"
	"sync"
	"time"

	"github.com/superdurable/dex/service/interpreter/interfaces"
)

type ActivityHeartbeat struct {
	provider interfaces.ActivityProvider
	ctx      context.Context
	done     chan struct{}
	waiter   sync.WaitGroup
}

func NewActivityHeartbeat(
	provider interfaces.ActivityProvider,
	ctx context.Context,
	info interfaces.ActivityInfo,
) *ActivityHeartbeat {
	if info.IsLocalActivity || info.HeartbeatTimeout <= 0 {
		return nil
	}
	heartbeat := &ActivityHeartbeat{
		provider: provider,
		ctx:      ctx,
		done:     make(chan struct{}),
	}
	heartbeat.waiter.Add(1)
	go heartbeat.run()
	return heartbeat
}

func (h *ActivityHeartbeat) run() {
	defer h.waiter.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.provider.RecordHeartbeat(h.ctx)
		case <-h.done:
			return
		case <-h.ctx.Done():
			return
		}
	}
}

func (h *ActivityHeartbeat) Stop() {
	if h == nil {
		return
	}
	close(h.done)
	h.waiter.Wait()
}
