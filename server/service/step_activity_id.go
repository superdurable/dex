// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package service

import "strings"

const (
	waitForStepActivityIDPrefix = DexSystemConstPrefix + "StepWaitFor_"
	executeStepActivityIDPrefix = DexSystemConstPrefix + "StepExecute_"
)

// WaitForStepActivityID identifies one Step wait activity across retries.
func WaitForStepActivityID(stepExecutionID string) string {
	return waitForStepActivityIDPrefix + stepExecutionID
}

// ExecuteStepActivityID identifies one Step execute activity across retries.
func ExecuteStepActivityID(stepExecutionID string) string {
	return executeStepActivityIDPrefix + stepExecutionID
}

// StepExecutionIDFromActivityID extracts IDs from Dex Step activities.
func StepExecutionIDFromActivityID(activityID string) (string, bool) {
	for _, prefix := range []string{waitForStepActivityIDPrefix, executeStepActivityIDPrefix} {
		if strings.HasPrefix(activityID, prefix) {
			return strings.TrimPrefix(activityID, prefix), true
		}
	}
	return "", false
}
