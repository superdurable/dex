// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesession

import (
	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/sdk-go/dex"
)

// sessionRPCContract is one RPC of the session contract, as agents/conformance/names.json records it.
type sessionRPCContract struct {
	name                string
	goMethodName        string
	isTransactional     bool
	lockedAttributes    []namedAttributeLock
	loadedAttributeMaps []namedAttributeDef
	loadedChannels      []namedChannelDef
	define              func(flow *RemoteSessionFlow, options *dex.RPCOptions) dex.RPCDef
}

type namedAttributeLock struct {
	name string
	lock dex.AttributeLock
}

type namedAttributeDef struct {
	name       string
	definition dex.AttributeDef
}

type namedChannelDef struct {
	name       string
	definition dex.ChannelDef
}

// sessionRPCContracts lists every session RPC with the options the Python and TypeScript cores register.
var sessionRPCContracts = []sessionRPCContract{
	{
		name:            dexagents.SendUserMessageRPCName,
		goMethodName:    "SendUserMessage",
		isTransactional: true,
		define: func(flow *RemoteSessionFlow, options *dex.RPCOptions) dex.RPCDef {
			return dex.DefineRPC(flow.SendUserMessage, options)
		},
	},
	{
		name:            SteerUserMessageRPCName,
		goMethodName:    "SteerUserMessage",
		isTransactional: true,
		loadedChannels:  []namedChannelDef{queuedUserMessagesLoad},
		define: func(flow *RemoteSessionFlow, options *dex.RPCOptions) dex.RPCDef {
			return dex.DefineRPC(flow.SteerUserMessage, options)
		},
	},
	{
		name:            DeleteQueuedMessageRPCName,
		goMethodName:    "DeleteQueuedMessage",
		isTransactional: true,
		loadedChannels:  []namedChannelDef{queuedUserMessagesLoad},
		define: func(flow *RemoteSessionFlow, options *dex.RPCOptions) dex.RPCDef {
			return dex.DefineRPC(flow.DeleteQueuedMessage, options)
		},
	},
	{
		name:         CancelTurnRPCName,
		goMethodName: "CancelTurn",
		lockedAttributes: []namedAttributeLock{
			lockAttribute(dexagents.PendingInterruptsAttribute),
			lockAttribute(dexagents.TurnCancellationRequestAttribute),
		},
		define: func(flow *RemoteSessionFlow, options *dex.RPCOptions) dex.RPCDef {
			return dex.DefineRPC(flow.CancelTurn, options)
		},
	},
	{
		name:             dexagents.ResolveInterruptRPCName,
		goMethodName:     "ResolveInterrupt",
		lockedAttributes: []namedAttributeLock{lockAttribute(dexagents.PendingInterruptsAttribute)},
		define: func(flow *RemoteSessionFlow, options *dex.RPCOptions) dex.RPCDef {
			return dex.DefineRPC(flow.ResolveInterrupt, options)
		},
	},
	{
		name:             AnswerQuestionRPCName,
		goMethodName:     "AnswerQuestion",
		lockedAttributes: []namedAttributeLock{lockAttribute(dexagents.PendingInterruptsAttribute)},
		define: func(flow *RemoteSessionFlow, options *dex.RPCOptions) dex.RPCDef {
			return dex.DefineRPC(flow.AnswerQuestion, options)
		},
	},
	{
		name:             ExecutePlanRPCName,
		goMethodName:     "ExecutePlan",
		lockedAttributes: []namedAttributeLock{lockAttribute(dexagents.PendingPlanExecutionAttribute)},
		define: func(flow *RemoteSessionFlow, options *dex.RPCOptions) dex.RPCDef {
			return dex.DefineRPC(flow.ExecutePlan, options)
		},
	},
	{
		name:            dexagents.UpdateSessionSettingsRPCName,
		goMethodName:    "UpdateSessionSettings",
		isTransactional: true,
		define: func(flow *RemoteSessionFlow, options *dex.RPCOptions) dex.RPCDef {
			return dex.DefineRPC(flow.UpdateSessionSettings, options)
		},
	},
	{
		name:         dexagents.GetAgentSnapshotRPCName,
		goMethodName: "GetAgentSnapshot",
		loadedAttributeMaps: []namedAttributeDef{
			loadAttributeMap(dexagents.AgentMessagesAttributeMap),
			loadAttributeMap(dexagents.AgentMessageChunksAttributeMap),
			loadAttributeMap(dexagents.AgentToolCallsAttributeMap),
		},
		loadedChannels: []namedChannelDef{queuedUserMessagesLoad, loadChannel(dexagents.SteeredUserMessagesChannel)},
		define: func(flow *RemoteSessionFlow, options *dex.RPCOptions) dex.RPCDef {
			return dex.DefineRPC(flow.GetAgentSnapshot, options)
		},
	},
	{
		name:         dexagents.ListAgentMessagesRPCName,
		goMethodName: "ListAgentMessages",
		loadedAttributeMaps: []namedAttributeDef{
			loadAttributeMap(dexagents.AgentMessagesAttributeMap),
			loadAttributeMap(dexagents.AgentMessageChunksAttributeMap),
		},
		define: func(flow *RemoteSessionFlow, options *dex.RPCOptions) dex.RPCDef {
			return dex.DefineRPC(flow.ListAgentMessages, options)
		},
	},
}

var queuedUserMessagesLoad = loadChannel(dexagents.QueuedUserMessagesChannel)

func (contract sessionRPCContract) rpcOptions() *dex.RPCOptions {
	options := &dex.RPCOptions{Name: contract.name, IsTransactional: contract.isTransactional}
	for _, locked := range contract.lockedAttributes {
		options.LockAttributes = append(options.LockAttributes, locked.lock)
	}
	for _, loaded := range contract.loadedAttributeMaps {
		options.LoadAttributeMaps = append(options.LoadAttributeMaps, loaded.definition)
	}
	for _, loaded := range contract.loadedChannels {
		options.LoadChannels = append(options.LoadChannels, loaded.definition)
	}
	return options
}

func lockAttribute[Value any](attribute dex.Attribute[Value]) namedAttributeLock {
	return namedAttributeLock{name: attribute.AttributeName(), lock: dex.LockAttribute(attribute)}
}

func loadAttributeMap[Value any](attributeMap dex.AttributeMap[Value]) namedAttributeDef {
	return namedAttributeDef{name: attributeMap.AttributeName(), definition: attributeMap}
}

func loadChannel[Value any](channel dex.Channel[Value]) namedChannelDef {
	return namedChannelDef{name: channel.ChannelName(), definition: channel}
}
