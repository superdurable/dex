// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package integTests

import (
	"github.com/xcherryio/sdk-go/integTests/basic"
	"github.com/xcherryio/sdk-go/integTests/failure_recovery"
	"github.com/xcherryio/sdk-go/integTests/local_attribute"
	"github.com/xcherryio/sdk-go/integTests/multi_states"
	"github.com/xcherryio/sdk-go/integTests/process_timeout"
	"github.com/xcherryio/sdk-go/integTests/state_decision"
	"github.com/xcherryio/sdk-go/integTests/stateretry"
	"github.com/xcherryio/sdk-go/xc"
)

var registry = xc.NewRegistry()

var client = xc.NewClient(registry, createTestDefaultClientOptions())

var workerService = xc.NewWorkerService(registry, nil)

func init() {
	err := registry.AddProcesses(
		&basic.IOProcess{},
		&failure_recovery.StateFailureRecoveryTestExecuteProcess{},
		&failure_recovery.StateFailureRecoveryTestWaitUntilProcess{},
		&failure_recovery.StateFailureRecoveryTestExecuteNoWaitUntilProcess{},
		&failure_recovery.StateFailureRecoveryTestExecuteFailedAtStartProcess{},
		&multi_states.MultiStatesProcess{},
		&local_attribute.LocalAttributeTestProcess{},
		&state_decision.GracefulCompleteProcess{},
		&state_decision.ForceCompleteProcess{},
		&state_decision.ForceFailProcess{},
		&state_decision.DeadEndProcess{},
		&stateretry.BackoffProcess{},
		//&global_attribute.SingleTableProcess{},
		//&global_attribute.MultiTablesProcess{},
		&process_timeout.TimeoutProcess{},
	)
	if err != nil {
		panic(err)
	}
}

func createTestDefaultClientOptions() *xc.ClientOptions {
	opts := xc.GetLocalDefaultClientOptions()
	opts.EnabledDebugLogging = true
	return opts
}
