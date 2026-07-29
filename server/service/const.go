// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package service

type (
	BackendType string
)

const (
	EnvNameDebugMode = "DEBUG_MODE"

	DefaultContinueAsNewPageSizeInBytes = 1024 * 1024

	TaskQueue = "Interpreter_DEFAULT"

	GetAttributesWorkflowQueryType   = "GetAttributes"
	GetCurrentTimerInfosQueryType    = "GetCurrentTimerInfos"
	ContinueAsNewDumpByPageQueryType = "ContinueAsNewDumpByPage"
	DebugDumpQueryType               = "DebugNewDump"
	PrepareRpcQueryType              = "PrepareRpcQueryType"

	ExecuteOptimisticLockingRpcUpdateType = "ExecuteOptimisticLockingRpcUpdate"
	WaitForStepCompletionUpdateType       = "WaitForStepCompletion"
	WaitForAttributeUpdateType            = "WaitForAttribute"

	SearchAttributeActiveStepTypes = "ActiveStepTypes"
	SearchAttributeDexWorkflowType = "FlowType"

	BackendTypeCadence  BackendType = "cadence"
	BackendTypeTemporal BackendType = "temporal"

	DexSystemConstPrefix = "__DexSystem_"

	SkipTimerSignalChannelName            = DexSystemConstPrefix + "SkipTimerChannel"
	FailWorkflowSignalChannelName         = DexSystemConstPrefix + "FailWorkflowChannel"
	UpdateConfigSignalChannelName         = DexSystemConstPrefix + "UpdateWorkflowConfig"
	ExecuteRpcSignalChannelName           = DexSystemConstPrefix + "ExecuteRpc"
	TriggerContinueAsNewSignalChannelName = DexSystemConstPrefix + "TriggerContinueAsNew"

	WorkerAddressMemoKey = DexSystemConstPrefix + "WorkerAddress"
	WorkflowRequestId    = DexSystemConstPrefix + "WorkflowRequestId"
)

const (
	GracefulCompletingFlowStepType = "_SYS_GRACEFUL_COMPLETING_FLOW"
	ForceCompletingFlowStepType    = "_SYS_FORCE_COMPLETING_FLOW"
	ForceFailingFlowStepType       = "_SYS_FORCE_FAILING_FLOW"
	DeadEndFlowStepType            = "_SYS_DEAD_END"
)

var ValidClosingFlowStepType = map[string]bool{
	GracefulCompletingFlowStepType: true,
	ForceCompletingFlowStepType:    true,
	ForceFailingFlowStepType:       true,
	DeadEndFlowStepType:            true,
}
