// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package defaults

import (
	"github.com/superdurable/dex/cli/internal/command/testfixtures/registered-type-names/defaults/helpers"
	"github.com/superdurable/dex/cli/internal/command/testfixtures/registered-type-names/model"
	"github.com/superdurable/dex/cli/internal/command/testfixtures/registered-type-names/versioned.v2"
	"github.com/superdurable/dex/sdk-go/dex"
)

const concatenatedStepTypePrefix = "fixture."

var Decisions = dex.DefineChannel[string]("decisions")

var prefersBranchingStepType = true

type Flow struct {
	dex.FlowDefaults
}

func (*Flow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(awaitDecision{}),
		dex.DefineStep(&pointerStep{}),
		dex.DefineStep(emptyOverrideStep{}),
		dex.DefineStep(concatenatedStep{}),
		dex.DefineStep(localConstantStep{}),
		dex.DefineStep(branchingOverrideStep{}),
		dex.DefineStep(promotedOverrideStep{}),
		dex.DefineStep(namesFileStep{}),
		dex.DefineStep(helperPromotedStep{}),
		dex.DefineStep(box[model.Input]{}),
		dex.DefineStep(pair[int, []string]{}),
		dex.DefineStep(byteBox[byte]{}),
		dex.DefineStep(anyBox[any]{}),
		dex.DefineStep(mapBox[map[string]*model.Input]{}),
		dex.DefineStep(nested[box[int]]{}),
		dex.DefineStep(versionedBox[versioned.Item]{}),
	}
}

func (*Flow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{Decisions}}
}

func (flow *Flow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*Flow) GetDexSummary(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*Flow) GetDexDisplay(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

// dex:group group-id:decision group-label:"Decision"
// dex:explanation text:"Wait for one decision."
type awaitDecision struct {
	dex.StepDefaults
}

func (awaitDecision) WaitFor(_ dex.Context, _ model.Input) (*dex.Wait, error) {
	return dex.AllOf(Decisions.ForOne()), nil
}

func (awaitDecision) Execute(_ dex.Context, input model.Input) (*dex.StepDecision, error) {
	return dex.GoTo(&pointerStep{}, input), nil
}

// dex:group group-id:overrides group-label:"Overrides"
// dex:explanation text:"Register a Step by pointer."
type pointerStep struct {
	dex.StepDefaultsNoWaitFor[model.Input]
}

func (*pointerStep) Execute(_ dex.Context, input model.Input) (*dex.StepDecision, error) {
	return dex.GoTo(emptyOverrideStep{}, input), nil
}

// dex:group group-id:overrides group-label:"Overrides"
// dex:explanation text:"Return an empty Step type to keep the default."
type emptyOverrideStep struct {
	dex.StepDefaultsNoWaitFor[model.Input]
}

func (emptyOverrideStep) GetStepType() string {
	return ""
}

func (emptyOverrideStep) Execute(_ dex.Context, input model.Input) (*dex.StepDecision, error) {
	return dex.GoTo(concatenatedStep{}, input), nil
}

// dex:group group-id:overrides group-label:"Overrides"
// dex:explanation text:"Concatenate constants into the Step type."
type concatenatedStep struct {
	dex.StepDefaultsNoWaitFor[model.Input]
}

func (concatenatedStep) GetStepType() string {
	return concatenatedStepTypePrefix + "ConcatenatedStep"
}

func (concatenatedStep) Execute(_ dex.Context, input model.Input) (*dex.StepDecision, error) {
	return dex.GoTo(localConstantStep{}, input), nil
}

// dex:group group-id:overrides group-label:"Overrides"
// dex:explanation text:"Return a constant declared inside the method."
type localConstantStep struct {
	dex.StepDefaultsNoWaitFor[model.Input]
}

func (localConstantStep) GetStepType() string {
	const localStepType = "LocalConstantStep"
	return localStepType
}

func (localConstantStep) Execute(_ dex.Context, input model.Input) (*dex.StepDecision, error) {
	return dex.GoTo(branchingOverrideStep{}, input), nil
}

// dex:group group-id:overrides group-label:"Overrides"
// dex:explanation text:"Return the same Step type from every branch."
type branchingOverrideStep struct {
	dex.StepDefaultsNoWaitFor[model.Input]
}

func (branchingOverrideStep) GetStepType() string {
	if prefersBranchingStepType {
		return "BranchingOverrideStep"
	} else {
		return "BranchingOverride" + "Step"
	}
}

func (branchingOverrideStep) Execute(_ dex.Context, input model.Input) (*dex.StepDecision, error) {
	return dex.GoTo(promotedOverrideStep{}, input), nil
}

type sameFileStepType struct{}

func (sameFileStepType) GetStepType() string {
	return "SameFilePromotedStep"
}

// dex:group group-id:overrides group-label:"Overrides"
// dex:explanation text:"Promote a Step type override declared in this file."
type promotedOverrideStep struct {
	dex.StepDefaultsNoWaitFor[model.Input]
	sameFileStepType
}

func (promotedOverrideStep) Execute(_ dex.Context, input model.Input) (*dex.StepDecision, error) {
	return dex.GoTo(namesFileStep{}, input), nil
}

// dex:group group-id:overrides group-label:"Overrides"
// dex:explanation text:"Declare the Step type override in another file."
type namesFileStep struct {
	dex.StepDefaultsNoWaitFor[model.Input]
}

func (namesFileStep) Execute(_ dex.Context, input model.Input) (*dex.StepDecision, error) {
	return dex.GoTo(helperPromotedStep{}, input), nil
}

// dex:group group-id:overrides group-label:"Overrides"
// dex:explanation text:"Promote a Step type override from another package."
type helperPromotedStep struct {
	dex.StepDefaultsNoWaitFor[model.Input]
	helpers.PromotedStepType
}

func (helperPromotedStep) Execute(_ dex.Context, input model.Input) (*dex.StepDecision, error) {
	return dex.GoTo(box[model.Input]{}, input), nil
}

// dex:group group-id:generics group-label:"Generics"
// dex:explanation text:"Instantiate a generic Step with an imported type."
type box[T any] struct {
	dex.StepDefaultsNoWaitFor[model.Input]
}

func (box[T]) Execute(_ dex.Context, input model.Input) (*dex.StepDecision, error) {
	return dex.GoTo(pair[int, []string]{}, input), nil
}

// dex:group group-id:generics group-label:"Generics"
// dex:explanation text:"Instantiate a generic Step with two type arguments."
type pair[First any, Second any] struct {
	dex.StepDefaultsNoWaitFor[model.Input]
}

func (pair[First, Second]) Execute(_ dex.Context, input model.Input) (*dex.StepDecision, error) {
	return dex.GoTo(byteBox[byte]{}, input), nil
}

// dex:group group-id:generics group-label:"Generics"
// dex:explanation text:"Instantiate a generic Step with byte."
type byteBox[T any] struct {
	dex.StepDefaultsNoWaitFor[model.Input]
}

func (byteBox[T]) Execute(_ dex.Context, input model.Input) (*dex.StepDecision, error) {
	return dex.GoTo(anyBox[any]{}, input), nil
}

// dex:group group-id:generics group-label:"Generics"
// dex:explanation text:"Instantiate a generic Step with any."
type anyBox[T any] struct {
	dex.StepDefaultsNoWaitFor[model.Input]
}

func (anyBox[T]) Execute(_ dex.Context, input model.Input) (*dex.StepDecision, error) {
	return dex.GoTo(mapBox[map[string]*model.Input]{}, input), nil
}

// dex:group group-id:generics group-label:"Generics"
// dex:explanation text:"Instantiate a generic Step with a map of pointers."
type mapBox[T any] struct {
	dex.StepDefaultsNoWaitFor[model.Input]
}

func (mapBox[T]) Execute(_ dex.Context, input model.Input) (*dex.StepDecision, error) {
	return dex.GoTo(nested[box[int]]{}, input), nil
}

// dex:group group-id:generics group-label:"Generics"
// dex:explanation text:"Instantiate a generic Step with a generic type argument."
type nested[T any] struct {
	dex.StepDefaultsNoWaitFor[model.Input]
}

func (nested[T]) Execute(_ dex.Context, input model.Input) (*dex.StepDecision, error) {
	return dex.GoTo(versionedBox[versioned.Item]{}, input), nil
}

// dex:group group-id:generics group-label:"Generics"
// dex:explanation text:"Instantiate a generic Step with a type from a dotted package directory."
type versionedBox[T any] struct {
	dex.StepDefaultsNoWaitFor[model.Input]
}

func (versionedBox[T]) Execute(_ dex.Context, _ model.Input) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}
