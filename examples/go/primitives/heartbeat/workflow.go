// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package heartbeat

import (
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

type HeartbeatFlow struct {
	dex.FlowDefaults
}

func NewHeartbeatFlow() *HeartbeatFlow {
	return &HeartbeatFlow{}
}

func (*HeartbeatFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(heartbeatStep{})}
}

func (*HeartbeatFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type heartbeatStep struct {
	dex.DefaultStepType
	dex.NoWaitFor[int]
}

func (heartbeatStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodTimeout: 60 * time.Second,
		HeartbeatTimeout:     10 * time.Second,
		ExecuteRetry:         &dex.RetryPolicy{MaximumAttempts: 3},
	}
}

func (step heartbeatStep) Execute(ctx dex.Context, batches int) (*dex.StepDecision, error) {
	completedBatches := 0
	if _, err := ctx.GetLastHeartbeatValue(&completedBatches); err != nil {
		return nil, err
	}
	for batch := completedBatches; batch < batches; batch++ {
		select {
		case <-ctx.Done():
			return dex.DeadEnd(), nil
		default:
		}
		time.Sleep(2 * time.Second)
		if err := ctx.RecordHeartbeat(batch + 1); err != nil {
			return nil, err
		}
	}
	return dex.GracefulComplete("processed"), nil
}

var _ dex.Flow = (*HeartbeatFlow)(nil)
