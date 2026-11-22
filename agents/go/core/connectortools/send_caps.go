// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectortools

import (
	"encoding/json"
	"math"

	"github.com/superdurable/dex-connectors-library/sdkgo"
	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	// maximumToolAttempts is the most CallTool attempts of a read or idempotent_write tool; a
	// connector's own policy can only narrow it.
	maximumToolAttempts = 3
	// manifestMinimumMaxSends is the smallest agent maxSends a manifest may declare; a tool
	// policy may also set 1.
	manifestMinimumMaxSends = 2
	// defaultLimitedMaxSends is the cap a limited definition without maxSends declares in the
	// Python and TypeScript Connector SDKs.
	defaultLimitedMaxSends = 3
)

// sendCapManifest is what a connector manifest's agent block says about resending.
type sendCapManifest struct {
	repeatOnUnknown any
	maxSends        any
}

// connectorSendCap returns the send cap of a Mutation that may resend a write whose outcome is
// unknown, or 0 when its calls must not resend.
//
// The manifest must say repeatOnUnknown limited with an integer maxSends from 2
// to 10, and the definition must declare limited with the same cap; a limited
// definition without maxSends declares 3. Any other combination fails closed to
// 0, so the call runs once.
func connectorSendCap(
	kind sdkgo.AgentOperationKind,
	manifest sendCapManifest,
	definitionRepeatOnUnknown sdkgo.RepeatOnUnknown,
	definitionMaxSends int32,
) int32 {
	manifestCap, isInteger := integralJSONNumber(manifest.maxSends)
	if kind != sdkgo.AgentOperationKindMutation || manifest.repeatOnUnknown != string(sdkgo.RepeatOnUnknownLimited) ||
		!isInteger || manifestCap < manifestMinimumMaxSends || manifestCap > int64(dexagents.ConnectorMaxSendsCeiling) {
		return 0
	}
	if definitionMaxSends == 0 && definitionRepeatOnUnknown == sdkgo.RepeatOnUnknownLimited {
		definitionMaxSends = defaultLimitedMaxSends
	}
	if definitionRepeatOnUnknown != sdkgo.RepeatOnUnknownLimited || int64(definitionMaxSends) != manifestCap {
		return 0
	}
	return int32(manifestCap)
}

// integralJSONNumber reads a decoded JSON number with no fractional part, such as 3 or 3.0.
func integralJSONNumber(value any) (int64, bool) {
	var number float64
	switch typed := value.(type) {
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case float64:
		number = typed
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	if math.IsInf(number, 0) || math.IsNaN(number) || number != math.Trunc(number) || math.Abs(number) > math.MaxInt32 {
		return 0, false
	}
	return int64(number), true
}

// isConnectorMaxSends reports whether a send cap is from 1 to dexagents.ConnectorMaxSendsCeiling.
func isConnectorMaxSends(maxSends int64) bool {
	return maxSends >= 1 && maxSends <= int64(dexagents.ConnectorMaxSendsCeiling)
}

// boundedResendAttempts returns the attempts one bounded resend call gets.
//
// They are the operation's attempts, or 3 when those are unlimited, raised to
// at least the manifest's send cap, plus the amount by which the call's cap
// exceeds the manifest's. A raised cap needs one attempt per send.
func boundedResendAttempts(operationAttempts int32, manifestCap int32, callCap int32) int32 {
	attempts := operationAttempts
	if attempts <= 0 {
		attempts = maximumToolAttempts
	}
	attempts = max(attempts, manifestCap)
	return attempts + max(callCap-manifestCap, 0)
}

// toolRetryPolicy narrows the operation's Execute retry policy to the tool Step's attempts.
func toolRetryPolicy(stepDefaults sdkgo.StepDefaults, sendCap int32) *dex.RetryPolicy {
	policy := stepDefaults.ExecuteRetry
	if policy == nil {
		policy = sdkgo.DefaultConnectorStepExecuteRetryPolicy()
	}
	attempts := policy.MaximumAttempts
	switch {
	case sendCap != 0:
		attempts = boundedResendAttempts(attempts, sendCap, sendCap)
	case attempts <= 0:
		attempts = maximumToolAttempts
	default:
		attempts = min(attempts, maximumToolAttempts)
	}
	return &dex.RetryPolicy{
		InitialInterval:    policy.InitialInterval,
		BackoffCoefficient: policy.BackoffCoefficient,
		MaximumInterval:    policy.MaximumInterval,
		MaximumAttempts:    attempts,
		TotalDuration:      policy.TotalDuration,
	}
}
