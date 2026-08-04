// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

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
