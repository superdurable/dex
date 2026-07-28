// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dex

type CommunicationMethodDef struct {
	Name                string // for signal and internal channel
	CommunicationMethod CommunicationMethod
	RPC                 RPC         // only for CommunicationMethodRPCMethod
	RPCOptions          *RPCOptions // only for CommunicationMethodRPCMethod
}

type CommunicationMethod string

const (
	CommunicationMethodSignalChannel   CommunicationMethod = "SignalChannel"
	CommunicationMethodInternalChannel CommunicationMethod = "InternalChannel"
	CommunicationMethodRPCMethod       CommunicationMethod = "RPCMethod"
)

func SignalChannelDef(channelName string) CommunicationMethodDef {
	return CommunicationMethodDef{
		Name:                channelName,
		CommunicationMethod: CommunicationMethodSignalChannel,
	}
}

func InternalChannelDef(channelName string) CommunicationMethodDef {
	return CommunicationMethodDef{
		Name:                channelName,
		CommunicationMethod: CommunicationMethodInternalChannel,
	}
}

func RPCMethodDef(rpc RPC, rpcOptions *RPCOptions) CommunicationMethodDef {
	return CommunicationMethodDef{
		CommunicationMethod: CommunicationMethodRPCMethod,
		RPC:                 rpc,
		RPCOptions:          rpcOptions,
	}
}
