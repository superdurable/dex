// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"github.com/superdurable/dex/sdk-go/dex"
)

var registry = dex.NewRegistry()
var client = dex.NewClient(registry, nil)
var workerService = dex.NewWorkerService(registry, nil)

func init() {
	err := registry.AddWorkflows(
		&abnormalExitWorkflow{},
		&basicWorkflow{},
		&proceedOnStateStartFailWorkflow{},
		&timerWorkflow{},
		&signalWorkflow{},
		&interStateWorkflow{},
		&persistenceWorkflow{},
		&forceFailWorkflow{},
		&stateApiFailWorkflow{},
		&stateApiTimeoutWorkflow{},
		&skipWaitUntilWorkflow{},
		skipWaitUntilWorkflow2{}, // test register by struct
		rpcWorkflow{},
		noStateWorkflow{},
		noStartStateWorkflow{},
		executeApiFailRecoveryWorkflow{},
	)
	if err != nil {
		panic(err)
	}
}
