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

	InterpreterWorkflowName      = "Interpreter"
	BlobStoreCleanupWorkflowName = "BlobStoreCleanup"

	GetAttributesWorkflowQueryType        = "GetAttributes"
	GetCurrentTimerInfosQueryType         = "GetCurrentTimerInfos"
	ContinueAsNewDumpByPageQueryType      = "ContinueAsNewDumpByPage"
	DebugDumpQueryType                    = "DebugNewDump"
	PrepareRpcQueryType                   = "PrepareRpcQueryType"

	ExecuteOptimisticLockingRpcUpdateType = "ExecuteOptimisticLockingRpcUpdate"
	WaitForStepCompletionUpdateType       = "WaitForStepCompletion"
	WaitForAttributeUpdateType            = "WaitForAttribute"
	IWFContinueAsNewPreemptedErrorType    = "IWF_CAN_PREEMPTED"
	IWFInvalidArgumentErrorType           = "IWF_INVALID_ARGUMENT"
	IWFFailedPreconditionErrorType        = "IWF_FAILED_PRECONDITION"
	IWFDeadlineExceededErrorType          = "IWF_DEADLINE_EXCEEDED"
	IWFRPCAcquireLockFailureErrorType     = "IWF_RPC_ACQUIRE_LOCK_FAILURE"

	SearchAttributeGlobalVersion     = "IwfGlobalWorkflowVersion"
	SearchAttributeExecutingStateIds = "IwfExecutingStateIds"
	SearchAttributeIwfWorkflowType   = "IwfWorkflowType"

	BackendTypeCadence  BackendType = "cadence"
	BackendTypeTemporal BackendType = "temporal"

	IwfSystemConstPrefix = "__IwfSystem_"

	SkipTimerSignalChannelName            = IwfSystemConstPrefix + "SkipTimerChannel"
	FailWorkflowSignalChannelName         = IwfSystemConstPrefix + "FailWorkflowChannel"
	CompleteFlowSignalChannelName         = IwfSystemConstPrefix + "CompleteFlowChannel"
	UpdateConfigSignalChannelName         = IwfSystemConstPrefix + "UpdateWorkflowConfig"
	ExecuteRpcSignalChannelName           = IwfSystemConstPrefix + "ExecuteRpc"
	TriggerContinueAsNewSignalChannelName = IwfSystemConstPrefix + "TriggerContinueAsNew"

	WorkerTargetMemoKey = IwfSystemConstPrefix + "WorkerTarget"
	WorkflowRequestId   = IwfSystemConstPrefix + "WorkflowRequestId"
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
