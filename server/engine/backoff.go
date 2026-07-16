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

package engine

import (
	"math"
	"time"

	"github.com/xcherryio/apis/goapi/xcapi"
)

func GetNextBackoff(
	completedAttempts int32, firstAttemptStartTimestampSeconds int64, policy *xcapi.RetryPolicy,
) (nextBackoffSeconds int32, shouldRetry bool) {
	policy = setDefaultRetryPolicyValue(policy)
	if *policy.MaximumAttempts > 0 && completedAttempts >= *policy.MaximumAttempts {
		return 0, false
	}
	nowSeconds := int64(time.Now().Unix())
	if *policy.MaximumAttemptsDurationSeconds > 0 && firstAttemptStartTimestampSeconds+int64(*policy.MaximumAttemptsDurationSeconds) < nowSeconds {
		return 0, false
	}
	initInterval := *policy.InitialIntervalSeconds
	nextInterval := int32(float64(initInterval) * math.Pow(float64(*policy.BackoffCoefficient), float64(completedAttempts-1)))
	if nextInterval > *policy.MaximumIntervalSeconds {
		nextInterval = *policy.MaximumIntervalSeconds
	}
	return nextInterval, true
}

func setDefaultRetryPolicyValue(policy *xcapi.RetryPolicy) *xcapi.RetryPolicy {
	if policy == nil {
		policy = &xcapi.RetryPolicy{}
	}
	if policy.InitialIntervalSeconds == nil {
		policy.InitialIntervalSeconds = defaultWorkerTaskBackoffRetryPolicy.InitialIntervalSeconds
	}
	if policy.BackoffCoefficient == nil {
		policy.BackoffCoefficient = defaultWorkerTaskBackoffRetryPolicy.BackoffCoefficient
	}
	if policy.MaximumIntervalSeconds == nil {
		policy.MaximumIntervalSeconds = defaultWorkerTaskBackoffRetryPolicy.MaximumIntervalSeconds
	}
	if policy.MaximumAttempts == nil {
		policy.MaximumAttempts = defaultWorkerTaskBackoffRetryPolicy.MaximumAttempts
	}
	if policy.MaximumAttemptsDurationSeconds == nil {
		policy.MaximumAttemptsDurationSeconds = defaultWorkerTaskBackoffRetryPolicy.MaximumAttemptsDurationSeconds
	}
	return policy
}
