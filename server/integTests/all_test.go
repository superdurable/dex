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
	"testing"

	"github.com/xcherryio/sdk-go/integTests/local_attribute"

	"github.com/xcherryio/sdk-go/integTests/basic"
	"github.com/xcherryio/sdk-go/integTests/failure_recovery"
	"github.com/xcherryio/sdk-go/integTests/multi_states"
	"github.com/xcherryio/sdk-go/integTests/process_timeout"
	"github.com/xcherryio/sdk-go/integTests/state_decision"
	"github.com/xcherryio/sdk-go/integTests/stateretry"
)

func TestIOProcess(t *testing.T) {
	basic.TestStartIOProcess(t, client)
}

func TestStateBackoffRetry(t *testing.T) {
	stateretry.TestBackoff(t, client)
}

func TestTerminateProcess(t *testing.T) {
	multi_states.TestTerminateMultiStatesProcess(t, client)
}

func TestStopProcessByFail(t *testing.T) {
	multi_states.TestFailMultiStatesProcess(t, client)
}

func TestStateDecision(t *testing.T) {
	state_decision.TestGracefulCompleteProcess(t, client)
	state_decision.TestForceCompleteProcess(t, client)
	state_decision.TestForceFailProcess(t, client)
	state_decision.TestDeadEndProcess(t, client)
}

func TestProcessIdReusePolicyDisallowReuse(t *testing.T) {
	basic.TestProcessIdReusePolicyDisallowReuse(t, client)
}

func TestProcessIdReusePolicyAllowIfNoRunning(t *testing.T) {
	basic.TestProcessIdReusePolicyAllowIfNoRunning(t, client)
}

func TestProcessIdReusePolicyTerminateIfRunning(t *testing.T) {
	basic.TestProcessIdReusePolicyTerminateIfRunning(t, client)
}

func TestProcessIdReusePolicyAllowIfPreviousExitAbnormallyCase1(t *testing.T) {
	basic.TestProcessIdReusePolicyAllowIfPreviousExitAbnormallyCase1(t, client)
}

func TestProcessIdReusePolicyAllowIfPreviousExitAbnormallyCase2(t *testing.T) {
	basic.TestProcessIdReusePolicyAllowIfPreviousExitAbnormallyCase2(t, client)
}

func TestStateFailureRecoveryExecute(t *testing.T) {
	failure_recovery.TestStateFailureRecoveryTestExecuteProcess(t, client)
}

func TestStateFailureRecoveryWaitUntil(t *testing.T) {
	failure_recovery.TestStateFailureRecoveryTestWaitUntilProcess(t, client)
}

func TestStateFailureRecoveryExecuteNoWaitUntil(t *testing.T) {
	failure_recovery.TestStateFailureRecoveryTestExecuteNoWaitUntilProcess(t, client)
}

func TestStateFailureRecoveryExecuteFailedAtStart(t *testing.T) {
	failure_recovery.TestStateFailureRecoveryTestExecuteFailedAtStartProcess(t, client)
}

//func TestGlobalAttributesWithSingleTable(t *testing.T) {
//	global_attribute.TestGlobalAttributesWithSingleTable(t, client)
//}
//
//func TestGlobalAttributesWithMultiTables(t *testing.T) {
//	global_attribute.TestGlobalAttributesWithMultiTables(t, client)
//}

func TestLocalAttributes(t *testing.T) {
	local_attribute.TestLocalAttributes(t, client)
}

func TestProcessTimeoutCase1(t *testing.T) {
	process_timeout.TestStartTimeoutProcessCase1(t, client)
}

func TestProcessTimeoutCase2(t *testing.T) {
	process_timeout.TestStartTimeoutProcessCase2(t, client)
}

func TestProcessTimeoutCase3(t *testing.T) {
	process_timeout.TestStartTimeoutProcessCase3(t, client)
}

func TestProcessTimeoutCase4(t *testing.T) {
	process_timeout.TestStartTimeoutProcessCase4(t, client)
}
