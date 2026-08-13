// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package service

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
	InvokeRpcContinueAsNewPreempted = "continue-as-new preempted RPC"
	WaitForStepCompletionUpdateType = "WaitForStepCompletion"
	WaitForAttributeUpdateType      = "WaitForAttribute"

	SearchAttributeActiveStepTypes = "ActiveStepTypes"
	SearchAttributeDexWorkflowType = "FlowType"

	BackendTypeCadence  BackendType = "cadence"
	BackendTypeTemporal BackendType = "temporal"

	DexSystemConstPrefix = "__DexSystem_"

	SkipTimerSignalChannelName            = DexSystemConstPrefix + "SkipTimerChannel"
	StopWorkflowSignalChannelName         = DexSystemConstPrefix + "StopWorkflowChannel"
	UpdateConfigSignalChannelName         = DexSystemConstPrefix + "UpdateWorkflowConfig"
	ExecuteRpcSignalChannelName           = DexSystemConstPrefix + "ExecuteRpc"
	TriggerContinueAsNewSignalChannelName = DexSystemConstPrefix + "TriggerContinueAsNew"

	WorkerAddressMemoKey = DexSystemConstPrefix + "WorkerAddress"
	WorkflowRequestId    = DexSystemConstPrefix + "WorkflowRequestId"
)
