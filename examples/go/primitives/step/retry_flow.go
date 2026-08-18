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

package step

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

type RetryFlow struct {
	dex.FlowDefaults
}

func NewRetryFlow() *RetryFlow {
	return &RetryFlow{}
}

func (*RetryFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(retryStep{})}
}

func (*RetryFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type retryStep struct {
	dex.DefaultStepType
	dex.NoWaitFor[int]
}

func (retryStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry: &dex.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumAttempts:    5,
		},
	}
}

func (retryStep) Execute(ctx dex.Context, readyAfterAttempt int) (*dex.StepDecision, error) {
	if ctx.Attempt() < int32(readyAfterAttempt) {
		return nil, fmt.Errorf("not ready on attempt %d", ctx.Attempt())
	}
	return dex.GracefulComplete("ready"), nil
}

var _ dex.Flow = (*RetryFlow)(nil)
