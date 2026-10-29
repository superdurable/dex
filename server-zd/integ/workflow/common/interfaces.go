// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package common

// TestResult holds worker invoke counters and arbitrary captured data for assertions.
type TestResult struct {
	InvokeHistory map[string]int64
	InvokeData    map[string]interface{}
}

// TestResultProvider exposes invoke results for integ assertions.
type TestResultProvider interface {
	GetTestResult() TestResult
}
