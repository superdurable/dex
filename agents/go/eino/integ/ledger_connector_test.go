// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/superdurable/dex-connectors-library/sdkgo"

	"github.com/superdurable/dex/agents/go/core/connectortools"
	"github.com/superdurable/dex/sdk-go/dex"
)

// ledgerConnectorID is the test Connector whose operations the connector agent calls as tools.
const ledgerConnectorID = "fake-ledger"

var ledgerConnection = sdkgo.ConnectionRef{Provider: ledgerConnectorID, Name: "test-ledger"}

// newLedgerToolSource returns the fake-ledger tools; each operation counts its runs in "<operation>.calls".
func newLedgerToolSource(coordination coordinationFiles) (*connectortools.ConnectorToolSource, error) {
	getBalance, err := sdkgo.NewQueryAgentTool[ledgerAccount, ledgerBalance](
		ledgerGetBalance{coordination: coordination}, ledgerConnection, ledgerGetBalanceMetadata())
	if err != nil {
		return nil, err
	}
	postNote, err := sdkgo.NewMutationAgentTool[ledgerNote, ledgerStored](
		ledgerPostNote{coordination: coordination}, ledgerConnection, ledgerPostNoteMetadata())
	if err != nil {
		return nil, err
	}
	createPayout, err := sdkgo.NewMutationAgentTool[ledgerPayout, ledgerStored](
		ledgerCreatePayout{coordination: coordination}, ledgerConnection, ledgerCreatePayoutMetadata())
	if err != nil {
		return nil, err
	}
	tools := make([]*connectortools.ConnectorTool, 0, 3)
	for _, agentTool := range []sdkgo.AgentTool{getBalance, postNote, createPayout} {
		connectorTool, err := connectortools.NewConnectorTool(agentTool)
		if err != nil {
			return nil, err
		}
		tools = append(tools, connectorTool)
	}
	return connectortools.NewConnectorToolSource(tools)
}

type ledgerAccount struct {
	AccountID string `json:"accountId"`
}

type ledgerBalance struct {
	AccountID string `json:"accountId"`
	Amount    int    `json:"amount"`
}

// ledgerNote is a note; the word "reject" makes the ledger refuse it.
type ledgerNote struct {
	AccountID string `json:"accountId"`
	Text      string `json:"text"`
}

type ledgerPayout struct {
	AccountID string `json:"accountId"`
	Amount    int    `json:"amount"`
}

type ledgerStored struct {
	ID string `json:"id,omitempty"`
}

func ledgerBranches(happyPath sdkgo.BranchID, optional ...sdkgo.BranchID) []sdkgo.BranchDefinition {
	defined := []sdkgo.BranchDefinition{{ID: happyPath, Description: "The " + string(happyPath) + " branch."}}
	for _, branch := range optional {
		defined = append(defined, sdkgo.BranchDefinition{ID: branch, Description: "The " + string(branch) + " branch.", Optional: true})
	}
	return defined
}

func ledgerStepDefaults() sdkgo.StepDefaults {
	return sdkgo.StepDefaults{ExecuteMethodTimeout: 30 * time.Second, ExecuteDurability: dex.StepDurabilitySync}
}

func ledgerFailure(operation string, kind sdkgo.FailureKind, message string) sdkgo.Failure {
	return sdkgo.Failure{Kind: kind, Provider: ledgerConnectorID, Operation: operation, Message: message}
}

// ledgerGetBalance is the Query getBalance.
type ledgerGetBalance struct {
	coordination coordinationFiles
}

func ledgerGetBalanceDefinition() sdkgo.QueryDefinition {
	return sdkgo.QueryDefinition{
		Operation:    sdkgo.OperationRef{ConnectorID: ledgerConnectorID, OperationID: "getBalance"},
		Branches:     ledgerBranches("found", sdkgo.DefectBranchID),
		StepDefaults: ledgerStepDefaults(),
	}
}

func (ledgerGetBalance) Definition() sdkgo.QueryDefinition { return ledgerGetBalanceDefinition() }

func (operation ledgerGetBalance) Invoke(_ sdkgo.Call, input ledgerAccount) sdkgo.QueryAttempt[ledgerBalance] {
	operation.coordination.recordCall("getBalance")
	return sdkgo.NewQueryBranch("found", ledgerBalance{AccountID: input.AccountID, Amount: 1250}, nil, sdkgo.Receipt{})
}

func ledgerGetBalanceMetadata() sdkgo.AgentToolMetadata {
	definition := ledgerGetBalanceDefinition()
	return sdkgo.AgentToolMetadata{
		Operation:   definition.Operation,
		Kind:        sdkgo.AgentOperationKindQuery,
		ToolName:    "fake-ledger_get_balance",
		Description: "Read the balance of one ledger account.",
		InputType:   "ledgerAccount",
		InputSchema: json.RawMessage(`{"type": "object", "additionalProperties": false, "properties": {` +
			`"accountId": {"type": "string", "description": "The account."}}, "required": ["accountId"]}`),
		Branches:     definition.Branches,
		StepDefaults: definition.StepDefaults,
	}
}

// ledgerPostNote is the single-attempt Mutation postNote.
type ledgerPostNote struct {
	coordination coordinationFiles
}

func ledgerPostNoteDefinition() sdkgo.MutationDefinition {
	return sdkgo.MutationDefinition{
		Operation:       sdkgo.OperationRef{ConnectorID: ledgerConnectorID, OperationID: "postNote"},
		Branches:        ledgerBranches("posted", "providerRejected", sdkgo.UncertainBranchID, sdkgo.DefectBranchID),
		StepDefaults:    ledgerStepDefaults(),
		RepeatOnUnknown: sdkgo.RepeatOnUnknownUnsafe,
	}
}

func (ledgerPostNote) Definition() sdkgo.MutationDefinition { return ledgerPostNoteDefinition() }

func (ledgerPostNote) IdempotencyKey(callID sdkgo.CallID, _ ledgerNote) sdkgo.IdempotencyKey {
	return sdkgo.IdempotencyKey(callID)
}

func (operation ledgerPostNote) Invoke(_ sdkgo.Call, input ledgerNote) sdkgo.MutationAttempt[ledgerStored] {
	operation.coordination.recordCall("postNote")
	if strings.Contains(input.Text, "reject") {
		refused := ledgerFailure("postNote", sdkgo.FailureProviderRejection, "account is closed")
		return sdkgo.NewMutationBranch("providerRejected", ledgerStored{}, &refused, sdkgo.Receipt{})
	}
	return sdkgo.NewMutationBranch("posted", ledgerStored{ID: "note-1"}, nil, sdkgo.Receipt{ProviderObjectID: "note-1"})
}

func ledgerPostNoteMetadata() sdkgo.AgentToolMetadata {
	definition := ledgerPostNoteDefinition()
	return sdkgo.AgentToolMetadata{
		Operation:       definition.Operation,
		Kind:            sdkgo.AgentOperationKindMutation,
		ToolName:        "fake-ledger_post_note",
		Description:     "Post a note on one ledger account.",
		RepeatOnUnknown: sdkgo.RepeatOnUnknownUnsafe,
		BranchOutcomes:  map[sdkgo.BranchID]sdkgo.AgentBranchOutcome{"providerRejected": sdkgo.AgentBranchOutcomeRejected},
		InputType:       "ledgerNote",
		InputSchema: json.RawMessage(`{"type": "object", "additionalProperties": false, "properties": {` +
			`"accountId": {"type": "string"}, "text": {"type": "string"}}, "required": ["accountId", "text"]}`),
		Branches:     definition.Branches,
		StepDefaults: definition.StepDefaults,
	}
}

// ledgerCreatePayout is the retry-safe Mutation createPayout, whose metadata recommends approval.
type ledgerCreatePayout struct {
	coordination coordinationFiles
}

func ledgerCreatePayoutDefinition() sdkgo.MutationDefinition {
	return sdkgo.MutationDefinition{
		Operation:       sdkgo.OperationRef{ConnectorID: ledgerConnectorID, OperationID: "createPayout"},
		Branches:        ledgerBranches("paid", sdkgo.UncertainBranchID, sdkgo.DefectBranchID),
		StepDefaults:    ledgerStepDefaults(),
		RepeatOnUnknown: sdkgo.RepeatOnUnknownSafe,
	}
}

func (ledgerCreatePayout) Definition() sdkgo.MutationDefinition {
	return ledgerCreatePayoutDefinition()
}

func (ledgerCreatePayout) IdempotencyKey(callID sdkgo.CallID, _ ledgerPayout) sdkgo.IdempotencyKey {
	return sdkgo.IdempotencyKey(callID)
}

func (operation ledgerCreatePayout) Invoke(_ sdkgo.Call, input ledgerPayout) sdkgo.MutationAttempt[ledgerStored] {
	operation.coordination.recordCall("createPayout")
	return sdkgo.NewMutationBranch("paid", ledgerStored{ID: "payout-" + input.AccountID}, nil, sdkgo.Receipt{})
}

func ledgerCreatePayoutMetadata() sdkgo.AgentToolMetadata {
	definition := ledgerCreatePayoutDefinition()
	return sdkgo.AgentToolMetadata{
		Operation:             definition.Operation,
		Kind:                  sdkgo.AgentOperationKindMutation,
		ToolName:              "fake-ledger_create_payout",
		Description:           "Pay an amount out of one ledger account.",
		IsApprovalRecommended: true,
		IsRetrySafe:           true,
		RepeatOnUnknown:       sdkgo.RepeatOnUnknownSafe,
		InputType:             "ledgerPayout",
		InputSchema: json.RawMessage(`{"type": "object", "additionalProperties": false, "properties": {` +
			`"accountId": {"type": "string"}, "amount": {"type": "integer", "minimum": 1}}, "required": ["accountId", "amount"]}`),
		Branches:     definition.Branches,
		StepDefaults: definition.StepDefaults,
	}
}
