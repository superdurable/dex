// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorsmcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex/sdk-go/dex"
)

// operationRunner runs one typed connector operation from JSON arguments.
type operationRunner interface {
	operationRef() sdkgo.OperationRef
	kind() operationKind
	branches() []sdkgo.BranchDefinition
	stepDefaults() sdkgo.StepDefaults
	inputType() reflect.Type
	// run decodes arguments and runs the operation. It returns the connector's RetryError unchanged.
	run(ctx dex.Context, connection sdkgo.ConnectionRef, arguments json.RawMessage) (operationResult, error)
}

// operationResult is the terminal branch an operation selected, with its value as JSON.
type operationResult struct {
	branch  sdkgo.BranchID
	value   json.RawMessage
	failure *sdkgo.Failure
}

// errArgumentsDoNotDecode means the arguments passed the schema but do not decode into the operation input.
var errArgumentsDoNotDecode = errors.New("arguments do not decode")

type queryRunner[IN, OUT any] struct {
	operation  sdkgo.Query[IN, OUT]
	definition sdkgo.QueryDefinition
}

type mutationRunner[IN, OUT any] struct {
	operation  sdkgo.Mutation[IN, OUT]
	definition sdkgo.MutationDefinition
}

func newQueryRunner[IN, OUT any](operation sdkgo.Query[IN, OUT]) operationRunner {
	return queryRunner[IN, OUT]{operation: operation, definition: operation.Definition()}
}

func newMutationRunner[IN, OUT any](operation sdkgo.Mutation[IN, OUT]) operationRunner {
	return mutationRunner[IN, OUT]{operation: operation, definition: operation.Definition()}
}

func (runner queryRunner[IN, OUT]) run(ctx dex.Context, connection sdkgo.ConnectionRef, arguments json.RawMessage) (operationResult, error) {
	input, err := decodeOperationInput[IN](arguments)
	if err != nil {
		return operationResult{}, err
	}
	result, err := sdkgo.RunQuery(ctx, runner.operation, connection, input)
	if err != nil {
		return operationResult{}, err
	}
	return newOperationResult(result.Branch, result.Value, result.Failure)
}

func (runner mutationRunner[IN, OUT]) run(ctx dex.Context, connection sdkgo.ConnectionRef, arguments json.RawMessage) (operationResult, error) {
	input, err := decodeOperationInput[IN](arguments)
	if err != nil {
		return operationResult{}, err
	}
	result, err := sdkgo.RunMutation(ctx, runner.operation, connection, input)
	if err != nil {
		return operationResult{}, err
	}
	return newOperationResult(result.Branch, result.Value, result.Failure)
}

func decodeOperationInput[IN any](arguments json.RawMessage) (IN, error) {
	var input IN
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, errArgumentsDoNotDecode
	}
	return input, nil
}

func newOperationResult(branch sdkgo.BranchID, value any, failure *sdkgo.Failure) (operationResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return operationResult{}, fmt.Errorf("encode connector result value: %w", err)
	}
	return operationResult{branch: branch, value: encoded, failure: failure}, nil
}

func (runner queryRunner[IN, OUT]) operationRef() sdkgo.OperationRef {
	return runner.definition.Operation
}
func (runner mutationRunner[IN, OUT]) operationRef() sdkgo.OperationRef {
	return runner.definition.Operation
}
func (queryRunner[IN, OUT]) kind() operationKind    { return operationKindQuery }
func (mutationRunner[IN, OUT]) kind() operationKind { return operationKindMutation }
func (runner queryRunner[IN, OUT]) branches() []sdkgo.BranchDefinition {
	return runner.definition.Branches
}
func (runner mutationRunner[IN, OUT]) branches() []sdkgo.BranchDefinition {
	return runner.definition.Branches
}
func (runner queryRunner[IN, OUT]) stepDefaults() sdkgo.StepDefaults {
	return runner.definition.StepDefaults
}
func (runner mutationRunner[IN, OUT]) stepDefaults() sdkgo.StepDefaults {
	return runner.definition.StepDefaults
}
func (queryRunner[IN, OUT]) inputType() reflect.Type    { return reflect.TypeFor[IN]() }
func (mutationRunner[IN, OUT]) inputType() reflect.Type { return reflect.TypeFor[IN]() }
