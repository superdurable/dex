// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
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

	TaskQueue = "DEFAULT"

	GetAttributesWorkflowQueryType      = "GetAttributes"
	GetChannelMessagesWorkflowQueryType = "GetChannelMessages"
	GetCurrentTimerInfosQueryType       = "GetCurrentTimerInfos"
	ContinueAsNewDumpByPageQueryType    = "ContinueAsNewDumpByPage"
	DebugDumpQueryType                  = "DebugNewDump"
	IsStepExecutionCompletedQueryType   = "IsStepExecutionCompleted"
	PrepareRpcQueryType                 = "PrepareRpcQueryType"

	DeleteChannelMessageUpdateType  = "DeleteChannelMessage"
	WaitForStepCompletionUpdateType = "WaitForStepCompletion"
	WaitForAttributeUpdateType      = "WaitForAttribute"

	SearchAttributeActiveStepTypes = "ActiveStepTypes"
	SearchAttributeDexParentFlowID = "DexParentFlowID"
	SearchAttributeDexWorkflowType = "FlowType"

	BackendTypeCadence  BackendType = "cadence"
	BackendTypeTemporal BackendType = "temporal"
)

// Compact wire values reduce history size because Temporal and Cadence persist these names repeatedly.
const (
	InvokeRpcUpdateType                   = "IRPC"
	WaitForMethodActivityType             = "IWaitForM"
	ExecuteMethodActivityType             = "IExecuteM"
	WorkerRPCActivityType                 = "IWRPC"
	SkipTimerSignalChannelName            = "SkipTimer"
	StopWorkflowSignalChannelName         = "StopWorkflow"
	UpdateConfigSignalChannelName         = "UpdateConfig"
	ExecuteRpcSignalChannelName           = "ERPC"
	TriggerContinueAsNewSignalChannelName = "TriggerContinueAsNew"
	SubFlowCompletionSignalChannelName    = "SubFlowCompletion"

	ReqId = "ReqId"
)

const (
	FlowTimeoutStepType        = "sys:timeout_handler"
	FlowTimeoutStepExecutionID = FlowTimeoutStepType + "-1"
)

// SubFlowID returns the durable child identity for one parent Step condition.
func SubFlowID(parentFlowID string, stepExecutionID string, index int32) string {
	return fmt.Sprintf("SubFlow:%s-%s-%d", parentFlowID, stepExecutionID, index)
}
