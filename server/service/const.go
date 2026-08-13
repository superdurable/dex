// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package service

import "fmt"

type (
	BackendType string
)

const (
	EnvNameDebugMode = "DEBUG_MODE"

	DefaultContinueAsNewPageSizeInBytes = 1024 * 1024

	TaskQueue = "Interpreter_DEFAULT"

	GetAttributesWorkflowQueryType    = "GetAttributes"
	GetCurrentTimerInfosQueryType     = "GetCurrentTimerInfos"
	ContinueAsNewDumpByPageQueryType  = "ContinueAsNewDumpByPage"
	DebugDumpQueryType                = "DebugNewDump"
	IsStepExecutionCompletedQueryType = "IsStepExecutionCompleted"
	PrepareRpcQueryType               = "PrepareRpcQueryType"

	InvokeRpcUpdateType             = "InvokeRpc"
	WaitForStepCompletionUpdateType = "WaitForStepCompletion"
	WaitForAttributeUpdateType      = "WaitForAttribute"

	SearchAttributeActiveStepTypes = "ActiveStepTypes"
	SearchAttributeDexParentFlowID = "DexParentFlowID"
	SearchAttributeDexWorkflowType = "FlowType"

	BackendTypeCadence  BackendType = "cadence"
	BackendTypeTemporal BackendType = "temporal"

	DexSystemConstPrefix = "__DexSystem_"

	SkipTimerSignalChannelName            = DexSystemConstPrefix + "SkipTimerChannel"
	StopWorkflowSignalChannelName         = DexSystemConstPrefix + "StopWorkflowChannel"
	UpdateConfigSignalChannelName         = DexSystemConstPrefix + "UpdateWorkflowConfig"
	ExecuteRpcSignalChannelName           = DexSystemConstPrefix + "ExecuteRpc"
	TriggerContinueAsNewSignalChannelName = DexSystemConstPrefix + "TriggerContinueAsNew"
	SubFlowCompletionSignalChannelName    = DexSystemConstPrefix + "SubFlowCompletion"

	WorkerAddressMemoKey = DexSystemConstPrefix + "WorkerAddress"
	WorkflowRequestId    = DexSystemConstPrefix + "WorkflowRequestId"
)

// SubFlowID returns the durable child identity for one parent Step condition.
func SubFlowID(parentFlowID string, stepExecutionID string, index int32) string {
	return fmt.Sprintf("SubFlow-%s-%s-%d", parentFlowID, stepExecutionID, index)
}
