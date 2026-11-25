// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const goDeclaredLocksSource = `package declaredlocks

import "github.com/superdurable/dex/sdk-go/dex"

var (
	total    = dex.DefineAttribute[int]("total", TOTAL_OPTIONS)
	items    = dex.DefineAttributeMap[int]("items", ITEMS_OPTIONS)
	cartLock = dex.DefineAttribute[bool]("cart-lock")
	spare    = dex.DefineAttribute[int]("spare")
	replies  = dex.DefineChannelWithOptions[string]("replies", REPLIES_OPTIONS)
	rooms    = dex.DefineChannelMapWithOptions[string]("rooms", dex.PublishesRequireAttributeLock(cartLock))
)

func attributeOptions() []dex.AttributeOption { return nil }

func sharedOption() dex.AttributeOption { return dex.WritesRequireLock() }

func unguarded() dex.Attribute[int] { return spare }

var defaultLocks = []dex.AttributeLock{dex.LockAttribute(cartLock)}

func init() {
	alias := defaultLocks
	alias[0] = dex.LockAttribute(total)
}

var lockTable =map[string][]dex.AttributeLock{"total": {dex.LockAttribute(total)}}

type CartFlow struct{ dex.FlowDefaults }

func (*CartFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(StartStep{}), dex.DefineStep(AddItemStep{})}
}

func (*CartFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{total, items, cartLock, spare},
		Channels:   []dex.ChannelDef{replies, rooms},
	}
}

func (flow *CartFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.Reply, RPC_OPTIONS),
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*CartFlow) GetDexSummary(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*CartFlow) GetDexDisplay(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*CartFlow) Reply(ctx dex.Context, input string) (*dex.RPCResult[string], error) {
	RPC_BODY
	return &dex.RPCResult[string]{Output: input}, nil
}

func (*CartFlow) HandleTimeout(ctx dex.Context) (*dex.StepDecision, error) {
	TIMEOUT_BODY
	return dex.ForceFail("timeout"), nil
}

// dex:group group-id:cart group-label:"Cart"
// dex:explanation text:"Start the cart."
type StartStep struct{ dex.StepDefaultsNoWaitFor[string] }

func (StartStep) Execute(ctx dex.Context, input string) (*dex.StepDecision, error) {
	return dex.GoTo(AddItemStep{}, input MOVE_OPTIONS), nil
}

// dex:group group-id:cart group-label:"Cart"
// dex:explanation text:"Add one item to the cart."
type AddItemStep struct{ dex.StepDefaults }

STEP_OPTIONS

func (AddItemStep) WaitFor(ctx dex.Context, input string) (*dex.Wait, error) {
	WAIT_BODY
	return dex.SkipWaitImmediately(), nil
}

func (AddItemStep) Execute(ctx dex.Context, input string) (*dex.StepDecision, error) {
	EXECUTE_BODY
	return dex.GracefulComplete(input), nil
}

EXTRA_SOURCE
`

type goDeclaredLocksCase struct {
	extraSource    string
	totalOptions   string
	itemsOptions   string
	repliesOptions string
	rpcOptions     string
	moveOptions    string
	stepOptions    string
	rpcBody        string
	timeoutBody    string
	waitBody       string
	executeBody    string
}

const goReadThenWriteTotal = "value, _ := total.Get(ctx)\n\t_ = total.Set(ctx, value+2)"

func goExecuteLocks(locks string) string {
	return "func (AddItemStep) GetStepOptions() *dex.StepOptions {\n\treturn &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{" + locks + "}}\n}"
}

func goRPCLocks(locks string) string {
	return "&dex.RPCOptions{LockAttributes: []dex.AttributeLock{" + locks + "}}"
}

func TestGoDeclaredLockDiagnostics(t *testing.T) {
	testCases := map[string]struct {
		source goDeclaredLocksCase
		// diagnostics lists "severity code" for the three declared-lock codes, in report order.
		diagnostics []string
		// messageFragment, when set, appears in the first listed diagnostic.
		messageFragment string
	}{
		"execute holds own lock": {
			source: goDeclaredLocksCase{totalOptions: "dex.WritesRequireLock()", stepOptions: goExecuteLocks("dex.LockAttribute(total)"), executeBody: "_ = total.Set(ctx, 1)"},
		},
		"wait for holds own lock": {
			source: goDeclaredLocksCase{
				totalOptions: "dex.WritesRequireLock()",
				stepOptions:  "func (AddItemStep) GetStepOptions() *dex.StepOptions {\n\treturn &dex.StepOptions{WaitForLockAttributes: []dex.AttributeLock{dex.LockAttribute(total)}}\n}",
				waitBody:     "_ = total.Set(ctx, 1)",
			},
		},
		"rpc holds own lock": {
			source: goDeclaredLocksCase{totalOptions: "dex.WritesRequireLock()", rpcOptions: goRPCLocks("dex.LockAttribute(total)"), rpcBody: "_ = total.Delete(ctx)"},
		},
		"map instance lock held": {
			source: goDeclaredLocksCase{itemsOptions: "dex.WritesRequireLock()", rpcOptions: goRPCLocks(`dex.LockAttributeMap(items, "apple")`), rpcBody: `_ = items.Set(ctx, "apple", 1)`},
		},
		"shared attribute lock held": {
			source: goDeclaredLocksCase{itemsOptions: "dex.WritesRequireAttributeLock(cartLock)", stepOptions: goExecuteLocks("dex.LockAttribute(cartLock)"), executeBody: "_ = items.Set(ctx, input, 1)"},
		},
		"channel publish lock held": {
			source: goDeclaredLocksCase{repliesOptions: "dex.PublishesRequireAttributeLock(cartLock)", rpcOptions: goRPCLocks("dex.LockAttribute(cartLock)"), rpcBody: `_ = replies.Publish(ctx, "hi")`},
		},
		"reads are not checked": {
			source: goDeclaredLocksCase{totalOptions: "dex.WritesRequireLock()", executeBody: "_, _ = total.Get(ctx)"},
		},
		"timeout handler is not checked": {
			source: goDeclaredLocksCase{totalOptions: "dex.WritesRequireLock()", timeoutBody: "_ = total.Set(ctx, 1)"},
		},
		"step without options": {
			source:          goDeclaredLocksCase{totalOptions: "dex.WritesRequireLock()", executeBody: "_ = total.Set(ctx, 1)"},
			diagnostics:     []string{"warning declared_lock_missing"},
			messageFragment: "Step AddItemStep Execute writes attribute total without the lock its declaration requires; add dex.LockAttribute(total) to ExecuteLockAttributes",
		},
		"rpc with nil options": {
			source:      goDeclaredLocksCase{totalOptions: "dex.WritesRequireLock()", rpcBody: "_ = total.Set(ctx, 1)"},
			diagnostics: []string{"warning declared_lock_missing"},
		},
		"lock on another attribute": {
			source:      goDeclaredLocksCase{totalOptions: "dex.WritesRequireLock()", rpcOptions: goRPCLocks("dex.LockAttribute(cartLock)"), rpcBody: "_ = total.Set(ctx, 1)"},
			diagnostics: []string{"warning declared_lock_missing"},
		},
		"map lock on another instance": {
			source:          goDeclaredLocksCase{itemsOptions: "dex.WritesRequireLock()", rpcOptions: goRPCLocks(`dex.LockAttributeMap(items, "pear")`), rpcBody: `_ = items.Set(ctx, "apple", 1)`},
			diagnostics:     []string{"warning declared_lock_missing"},
			messageFragment: `dex.LockAttributeMap(items, "apple")`,
		},
		"map write with dynamic key and no map lock": {
			source:      goDeclaredLocksCase{itemsOptions: "dex.WritesRequireLock()", rpcOptions: goRPCLocks("dex.LockAttribute(cartLock)"), rpcBody: "_ = items.Set(ctx, input, 1)"},
			diagnostics: []string{"warning declared_lock_missing"},
		},
		"channel publish without lock": {
			source:      goDeclaredLocksCase{repliesOptions: "dex.PublishesRequireAttributeLock(cartLock)", rpcBody: `_ = replies.Publish(ctx, "hi")`},
			diagnostics: []string{"warning declared_lock_missing"},
		},
		"movement lock does not count": {
			source: goDeclaredLocksCase{
				totalOptions: "dex.WritesRequireLock()",
				moveOptions:  ", dex.WithStepOptions(&dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(total)}})",
				executeBody:  "_ = total.Set(ctx, 1)",
			},
			diagnostics: []string{"warning declared_lock_missing"},
		},
		"dynamic registration locks": {
			source: goDeclaredLocksCase{
				totalOptions: "dex.WritesRequireLock()",
				stepOptions:  "func (AddItemStep) GetStepOptions() *dex.StepOptions {\n\treturn &dex.StepOptions{ExecuteLockAttributes: lockTable[\"total\"]}\n}",
				executeBody:  "_ = total.Set(ctx, 1)",
			},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"map write with dynamic key under map lock": {
			source:      goDeclaredLocksCase{itemsOptions: "dex.WritesRequireLock()", rpcOptions: goRPCLocks(`dex.LockAttributeMap(items, "apple")`), rpcBody: "_ = items.Set(ctx, input, 1)"},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"two writers without declaration": {
			source:          goDeclaredLocksCase{rpcBody: "_ = total.Set(ctx, 1)", executeBody: goReadThenWriteTotal},
			diagnostics:     []string{"warning lock_requirement_not_declared"},
			messageFragment: "attribute total is written by RPC Reply, Step AddItemStep Execute, at least one of which also reads it",
		},
		"blind writers are not nudged": {
			source: goDeclaredLocksCase{rpcBody: "_ = total.Set(ctx, 1)", executeBody: "_ = total.Set(ctx, 2)"},
		},
		"read then write without declaration": {
			source:          goDeclaredLocksCase{rpcBody: "value, _ := total.Get(ctx)\n\t_ = total.Set(ctx, value+1)"},
			diagnostics:     []string{"warning lock_requirement_not_declared"},
			messageFragment: "is both read and written by RPC Reply, and dexcli cannot prove that one exact registration lock covers it",
		},
		"read then write under own lock without declaration": {
			source: goDeclaredLocksCase{rpcOptions: goRPCLocks("dex.LockAttribute(total)"), rpcBody: "value, _ := total.Get(ctx)\n\t_ = total.Set(ctx, value+1)"},
		},
		"two locked writers without declaration": {
			source: goDeclaredLocksCase{rpcOptions: goRPCLocks("dex.LockAttribute(total)"), stepOptions: goExecuteLocks("dex.LockAttribute(total)"), rpcBody: "_ = total.Set(ctx, 1)", executeBody: "_ = total.Set(ctx, 2)"},
		},
		"one unlocked writer of two without declaration": {
			source:          goDeclaredLocksCase{rpcOptions: goRPCLocks("dex.LockAttribute(total)"), rpcBody: "_ = total.Set(ctx, 1)", executeBody: goReadThenWriteTotal},
			diagnostics:     []string{"warning lock_requirement_not_declared"},
			messageFragment: "and no lock is held by every writer",
		},
		"step writers alone are not nudged": {
			source: goDeclaredLocksCase{waitBody: "_ = total.Set(ctx, 1)", executeBody: "value, _ := total.Get(ctx)\n\t_ = total.Set(ctx, value+1)"},
		},
		"writers share another attribute's lock": {
			source: goDeclaredLocksCase{rpcOptions: goRPCLocks("dex.LockAttribute(cartLock)"), stepOptions: goExecuteLocks("dex.LockAttribute(cartLock)"), rpcBody: "_ = total.Set(ctx, 1)", executeBody: goReadThenWriteTotal},
		},
		"writers share one constant map instance lock": {
			source: goDeclaredLocksCase{rpcOptions: goRPCLocks(`dex.LockAttributeMap(items, "cart")`), stepOptions: goExecuteLocks(`dex.LockAttributeMap(items, "cart")`), rpcBody: "_ = total.Set(ctx, 1)", executeBody: goReadThenWriteTotal},
		},
		"writers hold locks of different map instances": {
			source:      goDeclaredLocksCase{rpcOptions: goRPCLocks(`dex.LockAttributeMap(items, "rpc")`), stepOptions: goExecuteLocks(`dex.LockAttributeMap(items, "step")`), rpcBody: "_ = total.Set(ctx, 1)", executeBody: goReadThenWriteTotal},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"writers hold different locks": {
			source:          goDeclaredLocksCase{rpcOptions: goRPCLocks("dex.LockAttribute(cartLock)"), stepOptions: goExecuteLocks("dex.LockAttribute(total)"), rpcBody: "_ = total.Set(ctx, 1)", executeBody: goReadThenWriteTotal},
			diagnostics:     []string{"warning lock_requirement_not_declared"},
			messageFragment: "and no lock is held by every writer",
		},
		"read in a helper counts as a read": {
			source: goDeclaredLocksCase{
				extraSource: "func readTotal(ctx dex.Context) int {\n\tvalue, _ := total.Get(ctx)\n\treturn value\n}",
				rpcBody:     "_ = total.Set(ctx, 1)",
				executeBody: "_ = total.Set(ctx, readTotal(ctx)+1)",
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"read in WaitFor and write in Execute under the same lock": {
			source: goDeclaredLocksCase{
				rpcOptions:  goRPCLocks("dex.LockAttribute(cartLock)"),
				stepOptions: "func (AddItemStep) GetStepOptions() *dex.StepOptions {\n\treturn &dex.StepOptions{\n\t\tWaitForLockAttributes: []dex.AttributeLock{dex.LockAttribute(cartLock)},\n\t\tExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(cartLock)},\n\t}\n}",
				waitBody:    "value, _ := total.Get(ctx)\n\t_ = spare.Set(ctx, value)",
				rpcBody:     "_ = total.Set(ctx, 1)",
				executeBody: "snapshot, _ := spare.Get(ctx)\n\t_ = total.Set(ctx, snapshot+1)",
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"timeout writer with unknown locks keeps the warning": {
			source:      goDeclaredLocksCase{rpcOptions: goRPCLocks("dex.LockAttribute(cartLock)"), rpcBody: "value, _ := total.Get(ctx)\n\t_ = total.Set(ctx, value+1)", timeoutBody: "_ = total.Set(ctx, 0)"},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"call-time lock in another argument does not count": {
			source: goDeclaredLocksCase{
				extraSource: "func keyFor(key string, _ dex.AttributeLock) string { return key }\n\nfunc invokeReply(client *dex.Client, flow *CartFlow, key string) error {\n\tvar output string\n\treturn client.InvokeRPCWithOptions(nil, \"cart\", flow.Reply, keyFor(key, dex.LockAttributeMap(items, key)), &output, dex.RPCInvokeOptions{\n\t\tLoadAttributeMapInstances: []dex.AttributeMapLoad{items.Load(key)},\n\t})\n}",
				rpcBody:     "value, _ := items.Get(ctx, input)\n\t_ = items.Set(ctx, input, value+1)",
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"single read-writer under another attribute's lock": {
			source: goDeclaredLocksCase{rpcOptions: goRPCLocks("dex.LockAttribute(cartLock)"), rpcBody: "value, _ := total.Get(ctx)\n\t_ = total.Set(ctx, value+1)"},
		},
		"call-time locks do not count, even when every call passes one": {
			source: goDeclaredLocksCase{
				extraSource: "func invokeReply(client *dex.Client, flow *CartFlow, key string) error {\n\tvar output string\n\treturn client.InvokeRPCWithOptions(nil, \"cart\", flow.Reply, key, &output, dex.RPCInvokeOptions{\n\t\tLockAttributeMapInstances: []dex.AttributeLock{dex.LockAttributeMap(items, key)},\n\t\tLoadAttributeMapInstances: []dex.AttributeMapLoad{items.Load(key)},\n\t})\n}",
				rpcBody:     "value, _ := items.Get(ctx, input)\n\t_ = items.Set(ctx, input, value+1)",
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"aliased call of the RPC method value without a lock": {
			source: goDeclaredLocksCase{
				extraSource: "func invokeReply(client *dex.Client, flow *CartFlow, key string) error {\n\tvar output string\n\tif err := client.InvokeRPCWithOptions(nil, \"cart\", flow.Reply, key, &output, dex.RPCInvokeOptions{\n\t\tLockAttributeMapInstances: []dex.AttributeLock{dex.LockAttributeMap(items, key)},\n\t\tLoadAttributeMapInstances: []dex.AttributeMapLoad{items.Load(key)},\n\t}); err != nil {\n\t\treturn err\n\t}\n\treply := flow.Reply\n\treturn client.InvokeRPCWithOptions(nil, \"cart\", reply, key, &output, dex.RPCInvokeOptions{})\n}",
				rpcBody:     "value, _ := items.Get(ctx, input)\n\t_ = items.Set(ctx, input, value+1)",
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"reads in both phases under the same lock": {
			source: goDeclaredLocksCase{
				rpcOptions:  goRPCLocks("dex.LockAttribute(cartLock)"),
				stepOptions: "func (AddItemStep) GetStepOptions() *dex.StepOptions {\n\treturn &dex.StepOptions{\n\t\tWaitForLockAttributes: []dex.AttributeLock{dex.LockAttribute(cartLock)},\n\t\tExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(cartLock)},\n\t}\n}",
				waitBody:    "value, _ := total.Get(ctx)\n\t_ = spare.Set(ctx, value)",
				rpcBody:     "_ = total.Set(ctx, 1)",
				executeBody: "current, _ := total.Get(ctx)\n\tsnapshot, _ := spare.Get(ctx)\n\tif current >= 0 {\n\t\t_ = total.Set(ctx, snapshot+1)\n\t}",
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"step options from a branching helper": {
			source: goDeclaredLocksCase{
				rpcOptions:  goRPCLocks("dex.LockAttribute(cartLock)"),
				extraSource: "var useLock bool\n\nfunc stepOptionsFor(locked bool) *dex.StepOptions {\n\tif locked {\n\t\treturn &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(cartLock)}}\n\t}\n\treturn nil\n}",
				stepOptions: "func (AddItemStep) GetStepOptions() *dex.StepOptions {\n\treturn stepOptionsFor(useLock)\n}",
				rpcBody:     "_ = total.Set(ctx, 1)",
				executeBody: goReadThenWriteTotal,
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"rpc options from a branching helper": {
			source: goDeclaredLocksCase{
				rpcOptions:  "rpcOptionsFor(useLock)",
				extraSource: "var useLock bool\n\nfunc rpcOptionsFor(locked bool) *dex.RPCOptions {\n\tif locked {\n\t\treturn &dex.RPCOptions{LockAttributes: []dex.AttributeLock{dex.LockAttribute(cartLock)}}\n\t}\n\treturn nil\n}",
				stepOptions: goExecuteLocks("dex.LockAttribute(cartLock)"),
				rpcBody:     "value, _ := total.Get(ctx)\n\t_ = total.Set(ctx, value+1)",
				executeBody: "_ = total.Set(ctx, 2)",
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"movement options passed through a variable": {
			source: goDeclaredLocksCase{
				rpcOptions:  goRPCLocks("dex.LockAttribute(cartLock)"),
				extraSource: "var overrideOption = dex.WithStepOptions(&dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(spare)}})",
				moveOptions: ", overrideOption",
				stepOptions: goExecuteLocks("dex.LockAttribute(cartLock)"),
				rpcBody:     "_ = total.Set(ctx, 1)",
				executeBody: goReadThenWriteTotal,
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"rpc registered through an alias": {
			source: goDeclaredLocksCase{
				rpcOptions:  goRPCLocks("dex.LockAttribute(cartLock)"),
				extraSource: "func (flow *CartFlow) alternateRPCs() []dex.RPCDef {\n\treply := flow.Reply\n\treturn []dex.RPCDef{dex.DefineRPC(reply, nil)}\n}",
				stepOptions: goExecuteLocks("dex.LockAttribute(cartLock)"),
				rpcBody:     "_ = total.Set(ctx, 1)",
				executeBody: goReadThenWriteTotal,
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"context passed to a helper keeps the warning": {
			source: goDeclaredLocksCase{
				extraSource: "func audit(ctx dex.Context) { _ = ctx }",
				rpcBody:     "_ = total.Set(ctx, 1)",
				executeBody: "audit(ctx)\n\t_ = total.Set(ctx, 2)",
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"unknown locks keep the warning": {
			source: goDeclaredLocksCase{
				rpcOptions:  goRPCLocks("dex.LockAttribute(total)"),
				stepOptions: "func (AddItemStep) GetStepOptions() *dex.StepOptions {\n\treturn &dex.StepOptions{ExecuteLockAttributes: lockTable[\"total\"]}\n}",
				rpcBody:     "_ = total.Set(ctx, 1)",
				executeBody: goReadThenWriteTotal,
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"last lock option wins": {
			source: goDeclaredLocksCase{
				totalOptions: "dex.WritesRequireLock(), dex.WritesRequireAttributeLock(cartLock)",
				stepOptions:  goExecuteLocks("dex.LockAttribute(cartLock)"),
				executeBody:  "_ = total.Set(ctx, 1)",
			},
		},
		"unreadable option after a lock option": {
			source:      goDeclaredLocksCase{totalOptions: "dex.WritesRequireAttributeLock(cartLock), sharedOption()", executeBody: "_ = total.Set(ctx, 1)"},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"unrelated option after a lock option": {
			source:      goDeclaredLocksCase{totalOptions: "dex.WritesRequireLock(), dex.Plaintext()", executeBody: "_ = total.Set(ctx, 1)"},
			diagnostics: []string{"warning declared_lock_missing"},
		},
		"local receiver that shadows the resource": {
			source:      goDeclaredLocksCase{totalOptions: "dex.WritesRequireLock()", executeBody: "total := unguarded()\n\t_ = total.Set(ctx, 1)"},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"aliased lock list changed elsewhere": {
			source: goDeclaredLocksCase{
				totalOptions: "dex.WritesRequireLock()",
				stepOptions:  "func (AddItemStep) GetStepOptions() *dex.StepOptions {\n\treturn &dex.StepOptions{ExecuteLockAttributes: defaultLocks}\n}",
				executeBody:  "_ = total.Set(ctx, 1)",
			},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"write in a function literal": {
			source:      goDeclaredLocksCase{totalOptions: "dex.WritesRequireLock()", executeBody: "write := func() { _ = total.Set(ctx, 1) }\n\t_ = write"},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"write in a constant-false branch": {
			source:      goDeclaredLocksCase{totalOptions: "dex.WritesRequireLock()", executeBody: "if false {\n\t\t_ = total.Set(ctx, 1)\n\t}"},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"lock element rewritten through a pointer": {
			source: goDeclaredLocksCase{
				totalOptions: "dex.WritesRequireLock()",
				stepOptions:  "func (AddItemStep) GetStepOptions() *dex.StepOptions {\n\tlock := dex.LockAttribute(cartLock)\n\talias := &lock\n\t*alias = dex.LockAttribute(total)\n\treturn &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{lock}}\n}",
				executeBody:  "_ = total.Set(ctx, 1)",
			},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"package receiver rebound": {
			source: goDeclaredLocksCase{
				totalOptions: "dex.WritesRequireLock()",
				extraSource:  "func rebind() { total = spare }",
				executeBody:  "_ = total.Set(ctx, 1)",
			},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"shared lock reference rebound": {
			source: goDeclaredLocksCase{
				itemsOptions: "dex.WritesRequireAttributeLock(cartLock)",
				extraSource:  "func rebind() { cartLock = dex.DefineAttribute[bool](\"other-lock\") }",
				rpcBody:      `_ = items.Set(ctx, "apple", 1)`,
			},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"write in a constant-false loop": {
			source:      goDeclaredLocksCase{totalOptions: "dex.WritesRequireLock()", executeBody: "for false {\n\t\t_ = total.Set(ctx, 1)\n\t}"},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"write in a helper called from a dead branch of another helper": {
			source: goDeclaredLocksCase{
				totalOptions: "dex.WritesRequireLock()",
				extraSource:  "func outer(ctx dex.Context) {\n\tif false {\n\t\tinner(ctx)\n\t}\n}\n\nfunc inner(ctx dex.Context) { _ = total.Set(ctx, 1) }",
				executeBody:  "outer(ctx)",
			},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"write in a direct helper": {
			source: goDeclaredLocksCase{
				totalOptions: "dex.WritesRequireLock()",
				extraSource:  "func inner(ctx dex.Context) { _ = total.Set(ctx, 1) }",
				executeBody:  "inner(ctx)",
			},
			diagnostics: []string{"warning declared_lock_missing"},
		},
		"lock only in an unreachable option return": {
			source: goDeclaredLocksCase{
				totalOptions: "dex.WritesRequireLock()",
				stepOptions:  "func (AddItemStep) GetStepOptions() *dex.StepOptions {\n\tif false {\n\t\treturn &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(total)}}\n\t}\n\treturn nil\n}",
				executeBody:  "_ = total.Set(ctx, 1)",
			},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"channel map publish without lock": {
			source:      goDeclaredLocksCase{rpcBody: `_ = rooms.Publish(ctx, "lobby", "hi")`},
			diagnostics: []string{"warning declared_lock_missing"},
		},
		"unreadable attribute options are neither checked nor nudged": {
			source: goDeclaredLocksCase{totalOptions: "attributeOptions()...", rpcBody: "value, _ := total.Get(ctx)\n\t_ = total.Set(ctx, value+1)", executeBody: "_ = total.Set(ctx, 2)"},
		},
		"single blind writer": {
			source: goDeclaredLocksCase{rpcBody: "_ = total.Set(ctx, 1)"},
		},
		"two writers with declaration": {
			source: goDeclaredLocksCase{totalOptions: "dex.WritesRequireLock()", rpcOptions: goRPCLocks("dex.LockAttribute(total)"), stepOptions: goExecuteLocks("dex.LockAttribute(total)"), rpcBody: "_ = total.Set(ctx, 1)", executeBody: "_ = total.Set(ctx, 2)"},
		},
	}
	for _, name := range slices.Sorted(maps.Keys(testCases)) {
		testCase := testCases[name]
		t.Run(name, func(t *testing.T) {
			graph := analyzeGoDeclaredLocksSource(t, testCase.source)
			var diagnostics []string
			var messages []string
			for _, diagnostic := range graph.Diagnostics {
				switch diagnostic.Code {
				case "declared_lock_missing", "declared_lock_unresolved", "lock_requirement_not_declared":
					diagnostics = append(diagnostics, diagnostic.Severity+" "+diagnostic.Code)
					messages = append(messages, diagnostic.Message)
				}
			}
			require.Equal(t, testCase.diagnostics, diagnostics, "%+v", graph.Diagnostics)
			if testCase.messageFragment != "" {
				require.Contains(t, messages[0], testCase.messageFragment)
			}
			hasError := slices.ContainsFunc(testCase.diagnostics, func(diagnostic string) bool { return strings.HasPrefix(diagnostic, "error ") })
			require.Equal(t, !hasError, graph.Valid, "%+v", graph.Diagnostics)
		})
	}
}

func analyzeGoDeclaredLocksSource(t *testing.T, source goDeclaredLocksCase) *Graph {
	t.Helper()
	rpcOptions := source.rpcOptions
	if rpcOptions == "" {
		rpcOptions = "nil"
	}
	replacer := strings.NewReplacer(
		"TOTAL_OPTIONS", source.totalOptions,
		"ITEMS_OPTIONS", source.itemsOptions,
		"REPLIES_OPTIONS", source.repliesOptions,
		"RPC_OPTIONS", rpcOptions,
		" MOVE_OPTIONS", source.moveOptions,
		"STEP_OPTIONS", source.stepOptions,
		"RPC_BODY", source.rpcBody,
		"TIMEOUT_BODY", source.timeoutBody,
		"WAIT_BODY", source.waitBody,
		"EXECUTE_BODY", source.executeBody,
		"EXTRA_SOURCE", source.extraSource,
	)
	directory := t.TempDir()
	sdkDirectory, err := filepath.Abs(filepath.Join("..", "..", "..", "sdk-go"))
	require.NoError(t, err)
	module := "module declaredlocks\n\ngo 1.26.0\n\nrequire github.com/superdurable/dex/sdk-go v0.0.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.ToSlash(sdkDirectory) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(module), 0o644))
	sourcePath := filepath.Join(directory, "workflow.go")
	require.NoError(t, os.WriteFile(sourcePath, []byte(replacer.Replace(goDeclaredLocksSource)), 0o644))
	graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "CartFlow"})
	require.NoError(t, err)
	return graph
}
