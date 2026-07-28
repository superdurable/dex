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

package retry

import (
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"go.temporal.io/sdk/temporal"
	"go.uber.org/cadence/workflow"
)

func ConvertCadenceWorkflowRetryPolicy(policy *dexpb.FlowRetryPolicy) *workflow.RetryPolicy {
	if policy == nil {
		return nil
	}
	initial, maxInterval, maxAttempts, backoff := flowRetryDefaults(policy)

	return &workflow.RetryPolicy{
		InitialInterval:    time.Second * time.Duration(initial),
		MaximumInterval:    time.Second * time.Duration(maxInterval),
		MaximumAttempts:    maxAttempts,
		BackoffCoefficient: float64(backoff),
	}
}

func ConvertCadenceActivityRetryPolicy(policy *dexpb.RetryPolicy) *workflow.RetryPolicy {
	// Cadence has no server-side default; Temporal does when RetryPolicy is omitted.
	// Match Temporal's defaults so waitFor/execute keep infinite backoff without options.
	if policy == nil {
		policy = &dexpb.RetryPolicy{}
	}
	initial, maxInterval, maxAttempts, backoff, totalDuration := activityRetryDefaults(policy)

	expirationInterval := time.Duration(0)
	if totalDuration > 0 {
		expirationInterval = time.Second * time.Duration(totalDuration)
	} else {
		expirationInterval = time.Hour * 24 * 365 * 1
	}

	return &workflow.RetryPolicy{
		InitialInterval:    time.Second * time.Duration(initial),
		MaximumInterval:    time.Second * time.Duration(maxInterval),
		MaximumAttempts:    maxAttempts,
		BackoffCoefficient: float64(backoff),
		ExpirationInterval: expirationInterval,
	}
}

func ConvertTemporalWorkflowRetryPolicy(policy *dexpb.FlowRetryPolicy) *temporal.RetryPolicy {
	if policy == nil {
		return nil
	}
	initial, maxInterval, maxAttempts, backoff := flowRetryDefaults(policy)

	return &temporal.RetryPolicy{
		InitialInterval:    time.Second * time.Duration(initial),
		MaximumInterval:    time.Second * time.Duration(maxInterval),
		MaximumAttempts:    maxAttempts,
		BackoffCoefficient: float64(backoff),
	}
}

func ConvertTemporalActivityRetryPolicy(policy *dexpb.RetryPolicy) *temporal.RetryPolicy {
	if policy == nil {
		return nil
	}
	initial, maxInterval, maxAttempts, backoff, _ := activityRetryDefaults(policy)

	return &temporal.RetryPolicy{
		InitialInterval:    time.Second * time.Duration(initial),
		MaximumInterval:    time.Second * time.Duration(maxInterval),
		MaximumAttempts:    maxAttempts,
		BackoffCoefficient: float64(backoff),
	}
}

func flowRetryDefaults(policy *dexpb.FlowRetryPolicy) (initial, maxInterval, maxAttempts int32, backoff float32) {
	initial = policy.GetInitialIntervalSeconds()
	if initial == 0 {
		initial = 1
	}
	backoff = policy.GetBackoffCoefficient()
	if backoff == 0 {
		backoff = 2
	}
	maxInterval = policy.GetMaximumIntervalSeconds()
	if maxInterval == 0 {
		maxInterval = 100
	}
	maxAttempts = policy.GetMaximumAttempts()
	return
}

func activityRetryDefaults(policy *dexpb.RetryPolicy) (initial, maxInterval, maxAttempts int32, backoff float32, totalDuration int32) {
	initial = policy.GetInitialIntervalSeconds()
	if initial == 0 {
		initial = 1
	}
	backoff = policy.GetBackoffCoefficient()
	if backoff == 0 {
		backoff = 2
	}
	maxInterval = policy.GetMaximumIntervalSeconds()
	if maxInterval == 0 {
		maxInterval = 100
	}
	maxAttempts = policy.GetMaximumAttempts()
	totalDuration = policy.GetTotalDurationSeconds()
	return
}
