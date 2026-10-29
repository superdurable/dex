// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"flag"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

func TestMain(tests *testing.M) {
	if os.Getenv("DEX_INTEG_FAKE_BINARY") == "1" {
		os.Exit(runFakeBinaryDex())
	}
	flag.Parse()
	os.Exit(tests.Run())
}
func newRequestID() string { return uuid.NewString() }
func userActiveStepExecutions(state *dexpb.GetFlowStateResponse) []*dexpb.ActiveStepExecutionState {
	executions := make([]*dexpb.ActiveStepExecutionState, 0, len(state.GetActiveStepExecutions()))
	for _, execution := range state.GetActiveStepExecutions() {
		if execution.GetStepType() != flowTimeoutStepType {
			executions = append(executions, execution)
		}
	}
	return executions
}

const flowTimeoutStepType = "sys:timeout_handler"

const flowTimeoutStepExecutionID = flowTimeoutStepType + "-1"
const workQueuePermissionsAttributeName = "DexWorkQueuePermissions"
const flowTypeAttributeName = "FlowType"

func rpcStepExecutionSource(rpcName string) string { return "__rpc/" + rpcName }

const startingStepExecutionID = "__start__"
