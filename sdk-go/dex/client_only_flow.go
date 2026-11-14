// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import "fmt"

// ClientOnlyFlow marks a Flow definition that only a Client uses.
//
// Embed it in a Flow type to describe a Flow type that Workers of another
// application, or of another language, serve. The Client needs a registered
// Flow to invoke its RPCs and read its Streams. A client-only Flow gives the
// Client those definitions without offering to serve them: it declares the
// Flow type, the RPCs with their names, input and output types, and
// RPCOptions, and the Attributes, Channels, and Streams that those options and
// reads reference. Its RPC methods are never called, so they only need the
// right signatures; return an error from each.
//
// The marker changes only what may serve the Flow:
//
//   - NewRegistry returns a FlowDefinitionError when a client-only Flow
//     declares any Step, because no Worker of this process runs it.
//   - NewWorker returns a ClientOnlyFlowError when its Registry holds a
//     client-only Flow, so the Worker never polls tasks it cannot run.
//   - Client calls such as InvokeRPC, ReadStream, ListStreamMessages,
//     GetAttributes, DescribeFlow, and StopFlow behave exactly as with any
//     other registered Flow.
//
// Do not start a run with Client.StartFlow and a client-only Flow. Without a
// starting Step, StartFlow sends no start Step, and the new run of that Flow
// type never executes the starting Step that its serving Workers define.
// Start runs through the application that serves the Flow type, or with a
// Flow value that declares its starting Step.
//
// The RPC options must match the serving Flow's options, because the Client
// sends the locks, state loads, and transaction mode with every call. Keep
// the client-only definition in sync with the serving application.
//
//	type RemoteOrderFlow struct {
//		dex.FlowDefaults
//		dex.ClientOnlyFlow
//	}
//
//	var RemoteOrderStatus = dex.DefineAttribute[string]("status")
//
//	func (RemoteOrderFlow) GetFlowType() string { return "OrderFlow" }
//
//	func (RemoteOrderFlow) GetSteps() []dex.StepDef { return nil }
//
//	func (RemoteOrderFlow) GetPersistenceSchema() dex.PersistenceSchema {
//		return dex.PersistenceSchema{Attributes: []dex.AttributeDef{RemoteOrderStatus}}
//	}
//
//	func (flow RemoteOrderFlow) GetRPCs() []dex.RPCDef {
//		return []dex.RPCDef{dex.DefineRPC(flow.Cancel, &dex.RPCOptions{
//			Name:           "cancel",
//			LockAttributes: []dex.AttributeLock{dex.LockAttribute(RemoteOrderStatus)},
//		})}
//	}
//
//	// Cancel types the cancel RPC that the OrderFlow Workers serve.
//	func (RemoteOrderFlow) Cancel(dex.Context, string) (*dex.RPCResult[bool], error) {
//		return nil, errors.New("RemoteOrderFlow is client-only")
//	}
//
//	registry, err := dex.NewRegistry([]dex.Flow{RemoteOrderFlow{}})
//	if err != nil {
//		return err
//	}
//	client, err := dex.NewClient(registry, cache, dex.ClientOptions{})
//	if err != nil {
//		return err
//	}
//	var isCancelled bool
//	err = client.InvokeRPC(ctx, "order-7", RemoteOrderFlow{}.Cancel, "customer request", &isCancelled)
//
// Since 1.45.2.
type ClientOnlyFlow struct{}

// isClientOnlyFlow seals the marker: only types that embed ClientOnlyFlow have it.
func (ClientOnlyFlow) isClientOnlyFlow() {}

type clientOnlyFlowMarker interface {
	isClientOnlyFlow()
}

// IsClientOnlyFlow reports whether flow embeds ClientOnlyFlow.
//
// It inspects the Go value only and does not need a Registry. Both a value and
// a pointer of a type that embeds ClientOnlyFlow report true.
//
//	if dex.IsClientOnlyFlow(flow) {
//		log.Printf("flow %s is served elsewhere", dex.GetFinalFlowType(flow))
//	}
//
// It returns true when flow embeds ClientOnlyFlow, directly or through another
// embedded type, and false for any other Flow, including a nil Flow.
//
// Since 1.45.2.
func IsClientOnlyFlow(flow Flow) bool {
	if nilInterface(flow) {
		return false
	}
	_, isClientOnly := flow.(clientOnlyFlowMarker)
	return isClientOnly
}

// ClientOnlyFlowError reports that NewWorker received a Registry that holds a client-only Flow.
//
// A Worker serves every Flow of its Registry, and a client-only Flow has no
// Steps and no RPC implementations to serve. Build the Worker from a Registry
// without client-only Flows, and keep the client-only Flows in the Registry of
// the Client that calls them.
//
//	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{})
//	var clientOnly *dex.ClientOnlyFlowError
//	if errors.As(err, &clientOnly) {
//		log.Printf("remove %s from the Worker's Registry", clientOnly.FlowType)
//	}
//
// When the Registry holds several client-only Flows, the error names the
// first Flow type in lexical order.
//
// Since 1.45.2.
type ClientOnlyFlowError struct {
	// FlowType is the Flow type of the client-only Flow that the Registry holds.
	FlowType string
}

// Error returns a message that names the client-only Flow type.
func (e *ClientOnlyFlowError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("dex: Flow type %s is client-only; a Worker cannot serve it", e.FlowType)
}
