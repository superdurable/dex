// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
)

func TestValidateStepOptionsHeartbeatTimeout(t *testing.T) {
	for _, timeoutSeconds := range []int32{0, 10, 60} {
		require.NoError(t, ValidateStepOptions(&dexpb.StepOptions{
			HeartbeatTimeoutSeconds: timeoutSeconds,
		}, 10*time.Second))
	}
	for _, timeoutSeconds := range []int32{-1, 1, 9} {
		require.ErrorContains(t, ValidateStepOptions(&dexpb.StepOptions{
			HeartbeatTimeoutSeconds: timeoutSeconds,
		}, 10*time.Second), "heartbeat timeout")
	}
	require.ErrorContains(t, ValidateStepOptions(&dexpb.StepOptions{
		ExecuteFailureProceedStepType: "recovery",
		ExecuteFailureProceedStepOptions: &dexpb.StepOptions{
			HeartbeatTimeoutSeconds: 9,
		},
	}, 10*time.Second), "heartbeat timeout")
	require.NoError(t, ValidateStepOptions(&dexpb.StepOptions{
		HeartbeatTimeoutSeconds: 2,
	}, 2*time.Second))
}
