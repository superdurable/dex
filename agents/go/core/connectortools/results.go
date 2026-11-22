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
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/superdurable/dex-connectors-library/sdkgo"
	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
)

// DefaultConnectorSourceName is the default SourceName of a ConnectorToolSource.
//
// Since 0.6.16.
const DefaultConnectorSourceName = "connectors"

// DefaultConnectorResultMaxBytes is the default bound, in bytes of UTF-8 text, on the result one
// connector tool call gives the model: 64 KiB.
//
// Since 0.6.16.
const DefaultConnectorResultMaxBytes = 64 * 1024

// ConnectorResultTruncatedNote is the note appended to a truncated result; {total_bytes} is the
// size of the full result. The text is the same in every core.
//
// Since 0.6.16.
const ConnectorResultTruncatedNote = "[Dex] truncated, {total_bytes} bytes"

// ConnectorBranchOutcome names what a connector tool call records for the branch its operation
// selected.
//
// Since 0.6.16.
type ConnectorBranchOutcome string

const (
	// ConnectorBranchOutcomeSucceeded is a required branch, such as sent or refunded. The record
	// is succeeded and the model reads the result value.
	ConnectorBranchOutcomeSucceeded ConnectorBranchOutcome = "succeeded"
	// ConnectorBranchOutcomeRejected is a branch the agent metadata lists as rejected, such as
	// providerRejected. Nothing took effect. The record is failed with an error the model reads,
	// and the session continues.
	ConnectorBranchOutcomeRejected ConnectorBranchOutcome = "rejected"
	// ConnectorBranchOutcomeFailed is an optional branch the agent metadata does not list,
	// including defect, of a Query or a Mutation. The record is failed.
	ConnectorBranchOutcomeFailed ConnectorBranchOutcome = "failed"
	// ConnectorBranchOutcomeUnknown is uncertain, or a branch listed as unknown. The write may
	// have taken effect. The record is outcome_unknown.
	ConnectorBranchOutcomeUnknown ConnectorBranchOutcome = "outcome_unknown"
)

// ClassifyConnectorBranch returns what a connector tool call records for one selected branch.
//
// The outcome comes only from the branch the connector returns, as in the
// Python and TypeScript cores. The connector reconciles a send whose outcome
// is unknown: it selects uncertain, or asks to retry, whenever a send may have
// happened. The rules apply in this order, for a Query and a Mutation alike:
//
//  1. uncertain is ConnectorBranchOutcomeUnknown, whatever agentBranches says
//     or whether it is required.
//  2. A required branch other than defect is ConnectorBranchOutcomeSucceeded.
//  3. A branch agentBranches lists as rejected is ConnectorBranchOutcomeRejected.
//  4. A branch agentBranches lists as unknown is ConnectorBranchOutcomeUnknown.
//  5. Any other branch, including defect, is ConnectorBranchOutcomeFailed.
//
// sdkgo's AgentToolAttempt.Outcome also observes the single-dispatch fence and
// marks more Mutation branches unknown; a ConnectorToolSource classifies with
// this function instead, so a session records the same status on a Worker of
// every core.
//
//	outcome := connectortools.ClassifyConnectorBranch("providerRejected",
//		[]sdkgo.BranchID{"refunded"}, nil) // ConnectorBranchOutcomeFailed
//
// requiredBranches are the operation's branches that are not optional, and
// agentBranches is the agent metadata's BranchOutcomes.
//
// Since 0.6.16.
func ClassifyConnectorBranch(
	branch sdkgo.BranchID,
	requiredBranches []sdkgo.BranchID,
	agentBranches map[sdkgo.BranchID]sdkgo.AgentBranchOutcome,
) ConnectorBranchOutcome {
	if branch == sdkgo.UncertainBranchID {
		return ConnectorBranchOutcomeUnknown
	}
	if branch != sdkgo.DefectBranchID {
		for _, required := range requiredBranches {
			if required == branch {
				return ConnectorBranchOutcomeSucceeded
			}
		}
	}
	switch agentBranches[branch] {
	case sdkgo.AgentBranchOutcomeRejected:
		return ConnectorBranchOutcomeRejected
	case sdkgo.AgentBranchOutcomeUnknown:
		return ConnectorBranchOutcomeUnknown
	default:
		return ConnectorBranchOutcomeFailed
	}
}

// BoundConnectorResult returns output whole, or its first maxResultBytes bytes and the truncation
// note.
//
// A cut inside a character drops that character's partial bytes. The kept
// part is followed by a newline and ConnectorResultTruncatedNote with the
// full size, and TruncatedOutputBytes holds that size. The text is the same
// in every core. maxResultBytes must be positive.
//
// Since 0.6.16.
func BoundConnectorResult(output string, maxResultBytes int) *dexagents.ToolExecutionResult {
	if len(output) <= maxResultBytes {
		return &dexagents.ToolExecutionResult{Output: output}
	}
	end := maxResultBytes
	for end > 0 && !utf8.RuneStart(output[end]) {
		end--
	}
	totalBytes := int64(len(output))
	note := strings.ReplaceAll(ConnectorResultTruncatedNote, "{total_bytes}", strconv.FormatInt(totalBytes, 10))
	return &dexagents.ToolExecutionResult{Output: output[:end] + "\n" + note, TruncatedOutputBytes: &totalBytes}
}

// FormatConnectorResult returns the text the model reads for one result value, the same in every
// core.
//
// Keys are sorted by code point, separators carry no spaces, text stays
// unescaped UTF-8, and a number with no fractional part below 2^53 is written
// as an integer. It returns an error when jsonText is not JSON.
//
// Since 0.6.16.
func FormatConnectorResult(jsonText string) (string, error) {
	parsed, err := pythonjson.ParseOrdered([]byte(jsonText))
	if err != nil {
		return "", err
	}
	return pythonjson.Marshal(integralNumbersAsIntegers(parsed),
		pythonjson.Options{IsCompact: true, ShouldSortKeys: true})
}

// integralNumbersAsIntegers writes 2.0 as 2, as the Python core does for a float with no fraction.
func integralNumbersAsIntegers(value any) any {
	switch typed := value.(type) {
	case json.Number:
		text := typed.String()
		if !strings.ContainsAny(text, ".eE") {
			return typed
		}
		number, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsInf(number, 0) || number != math.Trunc(number) || math.Abs(number) >= 1<<53 {
			return typed
		}
		return json.Number(strconv.FormatInt(int64(number), 10))
	case pythonjson.Object:
		converted := make(pythonjson.Object, len(typed))
		for index, member := range typed {
			converted[index] = pythonjson.Member{Key: member.Key, Value: integralNumbersAsIntegers(member.Value)}
		}
		return converted
	case []any:
		converted := make([]any, len(typed))
		for index, item := range typed {
			converted[index] = integralNumbersAsIntegers(item)
		}
		return converted
	default:
		return value
	}
}

// branchDetail is the error a non-succeeded call records, the same text in every core.
func branchDetail(operation sdkgo.OperationRef, branch sdkgo.BranchID, failure *sdkgo.Failure) string {
	detail := operation.ConnectorID + " " + operation.OperationID + " returned " + string(branch)
	if failure != nil {
		detail += ": " + string(failure.Kind) + ": " + failure.Message
	}
	return detail
}
