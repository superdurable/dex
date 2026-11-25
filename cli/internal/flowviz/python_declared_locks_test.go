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

const pythonDeclaredLocksSource = `from dex import Attribute, AttributeMap, Channel, Context, Flow, PersistenceSchema, RPCResult, Step, StepDecision, StepList, StepMovement, StepOptions, Wait, force_fail, go_to_many, graceful_complete, rpc

LOCK_TABLE = {"total": (Attribute("unused", int).lock(),)}


LOCKS = ()

MODULE_PRELUDE


def attribute_options():
    return {}


def locked_rpc(function):
    return function


class CartFlow(Flow[str]):
    cart_lock = Attribute("cart-lock", bool)
    total = Attribute("total", int, TOTAL_OPTIONS)
    items = AttributeMap("items", int, ITEMS_OPTIONS)
    replies = Channel("replies", str, REPLIES_OPTIONS)
    CLASS_EXTRA

    def __init__(self):
        self.start = StartStep()
        self.add_item = AddItemStep()

    def get_steps(self):
        return StepList.start_step(self.start).other_steps(self.add_item)

    def get_persistence_schema(self):
        return PersistenceSchema.of(self.cart_lock, self.total, self.items, self.replies)

    OUTER_DECORATOR@rpc(name="Reply"RPC_OPTIONS)
    def reply(self, context: Context, input: str) -> RPCResult[str]:
        RPC_BODY
        return RPCResult(input)

    def handle_timeout(self, context: Context) -> StepDecision:
        TIMEOUT_BODY
        return force_fail("timeout")


# dex:group group-id:cart group-label:"Cart"
# dex:explanation text:"Start the cart."
class StartStep(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return go_to_many(StepMovement.of(AddItemStep, input MOVE_OPTIONS))


# dex:group group-id:cart group-label:"Cart"
# dex:explanation text:"Add one item to the cart."
class AddItemStep(Step[str]):
STEP_OPTIONS

    def wait_for(self, context: Context, input: str) -> Wait:
        WAIT_BODY
        return Wait.skip_immediately()

    def execute(self, context: Context, input: str) -> StepDecision:
        EXECUTE_BODY
        return graceful_complete(input)


MODULE_EXTRA
`

type pythonDeclaredLocksCase struct {
	outerDecorator string
	modulePrelude  string
	classExtra     string
	moduleExtra    string
	totalOptions   string
	itemsOptions   string
	repliesOptions string
	rpcLocks       string
	moveOptions    string
	stepOptions    string
	rpcBody        string
	timeoutBody    string
	waitBody       string
	executeBody    string
}

const pythonBothPhaseCartLock = "    def get_step_options(self):\n        return StepOptions(wait_for_lock_attributes=(CartFlow.cart_lock.lock(),), execute_lock_attributes=(CartFlow.cart_lock.lock(),))\n"

const pythonReadThenWriteTotal = "CartFlow.total.set(context, CartFlow.total.get(context) + 2)"

func pythonStepLocks(field string, locks string) string {
	return "    def get_step_options(self):\n        return StepOptions(" + field + "=(" + locks + ",))\n"
}

func TestPythonDeclaredLockDiagnostics(t *testing.T) {
	testCases := map[string]struct {
		source pythonDeclaredLocksCase
		// diagnostics lists "severity code" for the three declared-lock codes, in report order.
		diagnostics []string
		// messageFragment, when set, appears in the first listed diagnostic.
		messageFragment string
		// hasOtherError marks a source that another analyzer rule already makes invalid.
		hasOtherError bool
	}{
		"execute holds own lock": {
			source: pythonDeclaredLocksCase{totalOptions: "writes_require_lock=True", stepOptions: pythonStepLocks("execute_lock_attributes", "CartFlow.total.lock()"), executeBody: "CartFlow.total.set(context, 1)"},
		},
		"wait for holds own lock": {
			source: pythonDeclaredLocksCase{totalOptions: "writes_require_lock=True", stepOptions: pythonStepLocks("wait_for_lock_attributes", "CartFlow.total.lock()"), waitBody: "CartFlow.total.set(context, 1)"},
		},
		"rpc holds own lock": {
			source: pythonDeclaredLocksCase{totalOptions: "writes_require_lock=True", rpcLocks: "total.lock()", rpcBody: "self.total.delete(context)"},
		},
		"map instance lock held": {
			source: pythonDeclaredLocksCase{itemsOptions: "writes_require_lock=True", rpcLocks: `items.lock("apple")`, rpcBody: `self.items.set(context, "apple", 1)`},
		},
		"shared attribute lock held": {
			source: pythonDeclaredLocksCase{itemsOptions: "writes_require_lock=cart_lock", rpcLocks: "cart_lock.lock()", rpcBody: "self.items.set(context, input, 1)"},
		},
		"channel publish lock held": {
			source: pythonDeclaredLocksCase{repliesOptions: "publishes_require_lock=cart_lock", rpcLocks: "cart_lock.lock()", rpcBody: `self.replies.publish(context, "hi")`},
		},
		"reads are not checked": {
			source: pythonDeclaredLocksCase{totalOptions: "writes_require_lock=True", rpcBody: "self.total.get(context)"},
		},
		"timeout handler is not checked": {
			source: pythonDeclaredLocksCase{totalOptions: "writes_require_lock=True", timeoutBody: "self.total.set(context, 1)"},
		},
		"step without options": {
			source:          pythonDeclaredLocksCase{totalOptions: "writes_require_lock=True", executeBody: "CartFlow.total.set(context, 1)"},
			diagnostics:     []string{"warning declared_lock_missing"},
			messageFragment: "Step AddItemStep execute writes attribute total without the lock its declaration requires; add CartFlow.total.lock() to execute_lock_attributes",
		},
		"rpc without locks": {
			source:      pythonDeclaredLocksCase{totalOptions: "writes_require_lock=True", rpcBody: "self.total.set(context, 1)"},
			diagnostics: []string{"warning declared_lock_missing"},
		},
		"lock on another attribute": {
			source:      pythonDeclaredLocksCase{totalOptions: "writes_require_lock=True", rpcLocks: "cart_lock.lock()", rpcBody: "self.total.set(context, 1)"},
			diagnostics: []string{"warning declared_lock_missing"},
		},
		"map lock on another instance": {
			source:          pythonDeclaredLocksCase{itemsOptions: "writes_require_lock=True", rpcLocks: `items.lock("pear")`, rpcBody: `self.items.set(context, "apple", 1)`},
			diagnostics:     []string{"warning declared_lock_missing"},
			messageFragment: `items.lock('apple')`,
		},
		"map write with dynamic key and no map lock": {
			source:      pythonDeclaredLocksCase{itemsOptions: "writes_require_lock=True", rpcLocks: "cart_lock.lock()", rpcBody: "self.items.set(context, input, 1)"},
			diagnostics: []string{"warning declared_lock_missing"},
		},
		"channel publish without lock": {
			source:      pythonDeclaredLocksCase{repliesOptions: "publishes_require_lock=cart_lock", rpcBody: `self.replies.publish(context, "hi")`},
			diagnostics: []string{"warning declared_lock_missing"},
		},
		"movement lock does not count": {
			source: pythonDeclaredLocksCase{
				totalOptions: "writes_require_lock=True",
				moveOptions:  ", options=StepOptions(execute_lock_attributes=(CartFlow.total.lock(),))",
				executeBody:  "CartFlow.total.set(context, 1)",
			},
			diagnostics: []string{"warning declared_lock_missing"},
		},
		"dynamic registration locks": {
			source: pythonDeclaredLocksCase{
				totalOptions: "writes_require_lock=True",
				stepOptions:  "    def get_step_options(self):\n        return StepOptions(execute_lock_attributes=LOCK_TABLE[\"total\"])\n",
				executeBody:  "CartFlow.total.set(context, 1)",
			},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"map write with dynamic key under map lock": {
			source:      pythonDeclaredLocksCase{itemsOptions: "writes_require_lock=True", rpcLocks: `items.lock("apple")`, rpcBody: "self.items.set(context, input, 1)"},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"two writers without declaration": {
			source:          pythonDeclaredLocksCase{rpcBody: "self.total.set(context, 1)", executeBody: pythonReadThenWriteTotal},
			diagnostics:     []string{"warning lock_requirement_not_declared"},
			messageFragment: "attribute total is written by RPC Reply, Step AddItemStep execute, at least one of them reads it, and no registration lock is held by every writer",
		},
		"blind writers are not nudged": {
			source: pythonDeclaredLocksCase{rpcBody: "self.total.set(context, 1)", executeBody: "CartFlow.total.set(context, 2)"},
		},
		"read then write without declaration": {
			source:          pythonDeclaredLocksCase{rpcBody: "self.total.set(context, self.total.get(context) + 1)"},
			diagnostics:     []string{"warning lock_requirement_not_declared"},
			messageFragment: "is both read and written by RPC Reply without a registration lock",
		},
		"read then write under own lock without declaration": {
			source: pythonDeclaredLocksCase{rpcLocks: "total.lock()", rpcBody: "self.total.set(context, self.total.get(context) + 1)"},
		},
		"two locked writers without declaration": {
			source: pythonDeclaredLocksCase{rpcLocks: "total.lock()", stepOptions: pythonStepLocks("execute_lock_attributes", "CartFlow.total.lock()"), rpcBody: "self.total.set(context, 1)", executeBody: "CartFlow.total.set(context, 2)"},
		},
		"one unlocked writer of two without declaration": {
			source:          pythonDeclaredLocksCase{rpcLocks: "total.lock()", rpcBody: "self.total.set(context, 1)", executeBody: pythonReadThenWriteTotal},
			diagnostics:     []string{"warning lock_requirement_not_declared"},
			messageFragment: "and no registration lock is held by every writer",
		},
		"step writers alone are not nudged": {
			source: pythonDeclaredLocksCase{waitBody: "CartFlow.total.set(context, 1)", executeBody: "CartFlow.total.set(context, CartFlow.total.get(context) + 1)"},
		},
		"writers share another attribute's lock": {
			source: pythonDeclaredLocksCase{rpcLocks: "cart_lock.lock()", stepOptions: pythonStepLocks("execute_lock_attributes", "CartFlow.cart_lock.lock()"), rpcBody: "self.total.set(context, 1)", executeBody: pythonReadThenWriteTotal},
		},
		"writers share one constant map instance lock": {
			source: pythonDeclaredLocksCase{rpcLocks: `items.lock("cart")`, stepOptions: pythonStepLocks("execute_lock_attributes", `CartFlow.items.lock("cart")`), rpcBody: "self.total.set(context, 1)", executeBody: pythonReadThenWriteTotal},
		},
		"writers hold locks of different map instances": {
			source:      pythonDeclaredLocksCase{rpcLocks: `items.lock("rpc")`, stepOptions: pythonStepLocks("execute_lock_attributes", `CartFlow.items.lock("step")`), rpcBody: "self.total.set(context, 1)", executeBody: pythonReadThenWriteTotal},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"writers hold different locks": {
			source:          pythonDeclaredLocksCase{rpcLocks: "cart_lock.lock()", stepOptions: pythonStepLocks("execute_lock_attributes", "CartFlow.total.lock()"), rpcBody: "self.total.set(context, 1)", executeBody: pythonReadThenWriteTotal},
			diagnostics:     []string{"warning lock_requirement_not_declared"},
			messageFragment: "and no registration lock is held by every writer",
		},
		"read in a helper counts as a read": {
			source: pythonDeclaredLocksCase{
				modulePrelude: "def read_total(context):\n    return CartFlow.total.get(context)",
				rpcBody:       "self.total.set(context, 1)",
				executeBody:   "CartFlow.total.set(context, read_total(context) + 1)",
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"context passed to an unknown call may read": {
			source: pythonDeclaredLocksCase{
				rpcBody:     "self.total.set(context, 1)",
				executeBody: "CartFlow.total.set(context, compute(context))",
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"read in wait_for and write in execute under the same lock": {
			source: pythonDeclaredLocksCase{
				rpcLocks:    "cart_lock.lock()",
				stepOptions: "    def get_step_options(self):\n        return StepOptions(wait_for_lock_attributes=(CartFlow.cart_lock.lock(),), execute_lock_attributes=(CartFlow.cart_lock.lock(),))\n",
				waitBody:    "CartFlow.items.set(context, \"snapshot\", CartFlow.total.get(context))",
				rpcBody:     "self.total.set(context, 1)",
				executeBody: "CartFlow.total.set(context, CartFlow.items.get(context, \"snapshot\") + 1)",
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"reads in both phases under the same lock": {
			source: pythonDeclaredLocksCase{
				rpcLocks:    "cart_lock.lock()",
				stepOptions: pythonBothPhaseCartLock,
				waitBody:    "CartFlow.items.set(context, \"snapshot\", CartFlow.total.get(context))",
				rpcBody:     "self.total.set(context, 1)",
				executeBody: "if CartFlow.total.get(context) >= 0:\n            CartFlow.total.set(context, CartFlow.items.get(context, \"snapshot\") + 1)",
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"unknown read in wait_for and write in execute": {
			source: pythonDeclaredLocksCase{
				rpcLocks:    "cart_lock.lock()",
				stepOptions: pythonBothPhaseCartLock,
				waitBody:    "CartFlow.items.set(context, \"snapshot\", compute(context))",
				rpcBody:     "self.total.set(context, 1)",
				executeBody: "CartFlow.total.set(context, CartFlow.items.get(context, \"snapshot\"))",
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"helper that calls another helper": {
			source: pythonDeclaredLocksCase{
				modulePrelude: "def outer(context):\n    return inner(context)\n\n\ndef inner(context):\n    return CartFlow.total.get(context)",
				rpcLocks:      "cart_lock.lock()",
				stepOptions:   pythonStepLocks("execute_lock_attributes", "CartFlow.cart_lock.lock()"),
				rpcBody:       "self.total.set(context, 1)",
				executeBody:   "CartFlow.total.set(context, outer(context) + 1)",
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"mutable named lock list": {
			source: pythonDeclaredLocksCase{
				modulePrelude: "STEP_LOCKS = [Attribute(\"cart-lock\", bool).lock()]",
				moduleExtra:   "STEP_LOCKS.clear()",
				rpcLocks:      "cart_lock.lock()",
				stepOptions:   "    def get_step_options(self):\n        return StepOptions(execute_lock_attributes=STEP_LOCKS)\n",
				rpcBody:       "self.total.set(context, 1)",
				executeBody:   pythonReadThenWriteTotal,
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"movement options through a Step alias": {
			source: pythonDeclaredLocksCase{
				moduleExtra: "ALIAS = AddItemStep\n\n\ndef move(value):\n    return StepMovement.of(ALIAS, value, options=StepOptions())",
				rpcLocks:    "cart_lock.lock()",
				stepOptions: pythonStepLocks("execute_lock_attributes", "CartFlow.cart_lock.lock()"),
				rpcBody:     "self.total.set(context, 1)",
				executeBody: pythonReadThenWriteTotal,
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"positional-only context passed to a helper": {
			source: pythonDeclaredLocksCase{
				modulePrelude: "def read_total(context):\n    return CartFlow.total.get(context)",
				classExtra:    "@rpc(name=\"Bump\")\n    def bump(self, context: Context, /, input: str) -> RPCResult[str]:\n        self.total.set(context, read_total(context) + 1)\n        return RPCResult(input)",
				rpcBody:       "self.total.set(context, 1)",
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"positional-only context with SDK calls under one lock": {
			source: pythonDeclaredLocksCase{
				classExtra: "@rpc(name=\"Bump\", lock_attributes=(cart_lock.lock(),))\n    def bump(self, context: Context, /, input: str) -> RPCResult[str]:\n        self.total.set(context, self.total.get(context) + 1)\n        return RPCResult(input)",
				rpcLocks:   "cart_lock.lock()",
				rpcBody:    "self.total.set(context, self.total.get(context) + 2)",
			},
		},
		"movement options from a call": {
			source: pythonDeclaredLocksCase{
				moduleExtra: "def move(value):\n    return StepMovement.of(AddItemStep, value, options=AddItemStep().get_step_options())",
				rpcLocks:    "cart_lock.lock()",
				stepOptions: pythonStepLocks("execute_lock_attributes", "CartFlow.cart_lock.lock()"),
				rpcBody:     "self.total.set(context, 1)",
				executeBody: pythonReadThenWriteTotal,
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"failure route inside get_step_options": {
			source: pythonDeclaredLocksCase{
				rpcLocks:    "cart_lock.lock()",
				stepOptions: "    def get_step_options(self):\n        return StepOptions(execute_lock_attributes=(CartFlow.cart_lock.lock(),)).on_execute_failure_proceed_to(AddItemStep, StepOptions())\n",
				rpcBody:     "self.total.set(context, 1)",
				executeBody: pythonReadThenWriteTotal,
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"StepOptions alias": {
			source: pythonDeclaredLocksCase{
				moduleExtra: "MovementOptions = StepOptions",
				rpcLocks:    "cart_lock.lock()",
				stepOptions: pythonStepLocks("execute_lock_attributes", "CartFlow.cart_lock.lock()"),
				rpcBody:     "self.total.set(context, 1)",
				executeBody: pythonReadThenWriteTotal,
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"timeout writer with unknown locks keeps the warning": {
			source:      pythonDeclaredLocksCase{rpcLocks: "cart_lock.lock()", rpcBody: "self.total.set(context, self.total.get(context) + 1)", timeoutBody: "self.total.set(context, 0)"},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"single read-writer under another attribute's lock": {
			source: pythonDeclaredLocksCase{rpcLocks: "cart_lock.lock()", rpcBody: "self.total.set(context, self.total.get(context) + 1)"},
		},
		"unknown locks keep the warning": {
			source: pythonDeclaredLocksCase{
				rpcLocks:    "total.lock()",
				stepOptions: "    def get_step_options(self):\n        return StepOptions(execute_lock_attributes=LOCK_TABLE[\"total\"])\n",
				rpcBody:     "self.total.set(context, 1)",
				executeBody: pythonReadThenWriteTotal,
			},
			diagnostics: []string{"warning lock_requirement_not_declared"},
		},
		"local lock list shadows the module list": {
			source: pythonDeclaredLocksCase{
				totalOptions: "writes_require_lock=True",
				stepOptions:  "    def get_step_options(self):\n        LOCKS = (CartFlow.total.lock(),)\n        return StepOptions(execute_lock_attributes=LOCKS)\n",
				executeBody:  "CartFlow.total.set(context, 1)",
			},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"receiver attribute of another object": {
			source: pythonDeclaredLocksCase{
				totalOptions:  "writes_require_lock=True",
				modulePrelude: "class Holder:\n    def __init__(self):\n        self.total = Attribute(\"holder-total\", int)",
				rpcBody:       "other = Holder()\n        other.total.set(context, 1)",
			},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"flow class attribute rebound": {
			source: pythonDeclaredLocksCase{
				totalOptions: "writes_require_lock=True",
				moduleExtra:  "CartFlow.total = CartFlow.cart_lock",
				executeBody:  "CartFlow.total.set(context, True)",
			},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"class lock bound after the declaration": {
			source: pythonDeclaredLocksCase{
				modulePrelude: "guard = Attribute(\"module-lock\", bool)",
				totalOptions:  "writes_require_lock=guard",
				classExtra:    "guard = Attribute(\"class-lock\", bool)",
				stepOptions:   "    def get_step_options(self):\n        return StepOptions(execute_lock_attributes=(guard.lock(),))\n",
				executeBody:   "CartFlow.total.set(context, 1)",
			},
		},
		"write after a short-circuit operand": {
			source:      pythonDeclaredLocksCase{totalOptions: "writes_require_lock=True", executeBody: "False and CartFlow.total.set(context, 1)"},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"named lock list changed in place": {
			source: pythonDeclaredLocksCase{
				totalOptions:  "writes_require_lock=True",
				moduleExtra:   "ALIAS = LOCKS_FOR_STEP\nALIAS.append(CartFlow.total.lock())",
				modulePrelude: "LOCKS_FOR_STEP = []",
				stepOptions:   "    def get_step_options(self):\n        return StepOptions(execute_lock_attributes=LOCKS_FOR_STEP)\n",
				executeBody:   "CartFlow.total.set(context, 1)",
			},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"rpc suggestion uses the class-body name": {
			source:          pythonDeclaredLocksCase{repliesOptions: "publishes_require_lock=cart_lock", rpcBody: `self.replies.publish(context, "hi")`},
			diagnostics:     []string{"warning declared_lock_missing"},
			messageFragment: "add cart_lock.lock() to the rpc lock_attributes",
		},
		"write in a nested function": {
			source:      pythonDeclaredLocksCase{totalOptions: "writes_require_lock=True", executeBody: "def inner():\n            CartFlow.total.set(context, 1)\n        inner"},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"write in a constant-false branch": {
			source:      pythonDeclaredLocksCase{totalOptions: "writes_require_lock=True", executeBody: "if False:\n            CartFlow.total.set(context, 1)"},
			diagnostics: []string{"warning declared_lock_unresolved"},
		},
		"outer rpc decorator replaces the inner registration": {
			source:      pythonDeclaredLocksCase{outerDecorator: "@rpc(name=\"Reply\")\n    ", totalOptions: "writes_require_lock=True", rpcLocks: "total.lock()", rpcBody: "self.total.set(context, 1)"},
			diagnostics: []string{"warning declared_lock_missing"},
		},
		"unknown outer decorator": {
			source:        pythonDeclaredLocksCase{outerDecorator: "@locked_rpc\n    ", totalOptions: "writes_require_lock=True", rpcBody: "self.total.set(context, 1)"},
			diagnostics:   []string{"warning declared_lock_unresolved"},
			hasOtherError: true,
		},
		"positional publish lock": {
			source:      pythonDeclaredLocksCase{repliesOptions: "cart_lock", rpcBody: `self.replies.publish(context, "hi")`},
			diagnostics: []string{"warning declared_lock_missing"},
		},
		"explicit None publish lock": {
			source: pythonDeclaredLocksCase{repliesOptions: "publishes_require_lock=None", rpcBody: `self.replies.publish(context, "hi")`},
		},
		"map lock with instance keyword": {
			source: pythonDeclaredLocksCase{itemsOptions: "writes_require_lock=True", rpcLocks: `items.lock(instance="apple")`, rpcBody: `self.items.delete(context, "apple")`},
		},
		"unreadable attribute options are neither checked nor nudged": {
			source: pythonDeclaredLocksCase{totalOptions: "**attribute_options()", rpcBody: "self.total.set(context, self.total.get(context) + 1)", executeBody: "CartFlow.total.set(context, 2)"},
		},
		"single blind writer": {
			source: pythonDeclaredLocksCase{rpcBody: "self.total.set(context, 1)"},
		},
		"two writers with declaration": {
			source: pythonDeclaredLocksCase{
				totalOptions: "writes_require_lock=True",
				rpcLocks:     "total.lock()",
				stepOptions:  pythonStepLocks("execute_lock_attributes", "CartFlow.total.lock()"),
				rpcBody:      "self.total.set(context, 1)",
				executeBody:  "CartFlow.total.set(context, 2)",
			},
		},
	}
	for _, name := range slices.Sorted(maps.Keys(testCases)) {
		testCase := testCases[name]
		t.Run(name, func(t *testing.T) {
			graph := analyzePythonDeclaredLocksSource(t, testCase.source)
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
			require.Equal(t, !hasError && !testCase.hasOtherError, graph.Valid, "%+v", graph.Diagnostics)
		})
	}
}

func analyzePythonDeclaredLocksSource(t *testing.T, source pythonDeclaredLocksCase) *Graph {
	t.Helper()
	rpcOptions := ""
	if source.rpcLocks != "" {
		rpcOptions = ", lock_attributes=(" + source.rpcLocks + ",)"
	}
	resourceOptions := func(options string) string {
		if options == "" {
			return ""
		}
		return ", " + options
	}
	stepOptions := source.stepOptions
	if stepOptions == "" {
		stepOptions = "    pass\n"
	}
	statement := func(body string) string {
		if body == "" {
			return "pass"
		}
		return body
	}
	replacer := strings.NewReplacer(
		", TOTAL_OPTIONS", resourceOptions(source.totalOptions),
		", ITEMS_OPTIONS", resourceOptions(source.itemsOptions),
		", REPLIES_OPTIONS", resourceOptions(source.repliesOptions),
		"OUTER_DECORATOR", source.outerDecorator,
		"MODULE_PRELUDE", source.modulePrelude,
		"CLASS_EXTRA", source.classExtra,
		"MODULE_EXTRA", source.moduleExtra,
		"RPC_OPTIONS", rpcOptions,
		" MOVE_OPTIONS", source.moveOptions,
		"STEP_OPTIONS\n", stepOptions,
		"RPC_BODY", statement(source.rpcBody),
		"TIMEOUT_BODY", statement(source.timeoutBody),
		"WAIT_BODY", statement(source.waitBody),
		"EXECUTE_BODY", statement(source.executeBody),
	)
	sourcePath := filepath.Join(t.TempDir(), "workflow.py")
	require.NoError(t, os.WriteFile(sourcePath, []byte(replacer.Replace(pythonDeclaredLocksSource)), 0o644))
	graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "CartFlow"})
	require.NoError(t, err)
	return graph
}
