// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

// Flow is the top-level durable workflow definition. Any long-lived business
// object (at least a few seconds) can be modeled as a Flow: a named type, a
// list of Steps, and a persistence schema of attributes and channels.
//
// Exported methods on the Flow (other than the Flow interface methods) that
// match RPC[IN, OUT] are registered as RPCs under their Go method names.
//
// Example:
//
//	type OrderFlow struct{}
//
//	func (OrderFlow) GetFlowType() string { return "order" }
//
//	func (OrderFlow) GetSteps() []dex.StepDef {
//		return []dex.StepDef{
//			dex.DefineStepAsStart(ApproveOrder),
//			dex.DefineStep(ShipOrder),
//		}
//	}
//
//	func (OrderFlow) GetPersistenceSchema() dex.PersistenceSchema {
//		return dex.PersistenceSchema{
//			Attributes: []dex.AttributeDef{OrderStatus},
//			Channels:   []dex.ChannelDef{ApprovalChannel},
//		}
//	}
//
//	func (OrderFlow) GetSnapshot(
//		ctx dex.Context,
//		input GetSnapshotInput,
//	) (dex.RPCResult[OrderSnapshot], error) {
//		return dex.RPCResult[OrderSnapshot]{Output: OrderSnapshot{}}, nil
//	}
//
//	var Orders = OrderFlow{}
//	var _ dex.Flow = Orders
type Flow interface {
	// GetFlowType returns the durable flow type name used to start and look up
	// runs. It must be non-empty and unique among registered flows. Prefer a
	// stable explicit string; renaming the Go type does not change existing
	// runs that already stored this name.
	GetFlowType() string

	// GetSteps defines the steps of the flow. A step is one node in the flow
	// state machine: it may WaitFor conditions (channel / timer) and then
	// Execute a decision. See Step for details.
	//
	// Use DefineStepAsStart for at most one starting step and DefineStep for
	// every other step. An empty list, or a list with no starting step, means
	// the run starts with no step execution; application code can still invoke
	// RPCs that move into steps later.
	GetSteps() []StepDef

	// GetPersistenceSchema declares attributes and channels for this flow.
	//
	// Attributes can be read and written in WaitFor, Execute, and RPC. External
	// clients can also read them through Client APIs. Indexed attributes can be
	// used in SearchFlows queries.
	//
	// Channels carry messages into a waiting step: external clients publish;
	// WaitFor requests consumption and Execute reads results via
	// Channel.GetConditionResults. Channel maps key messages by string.
	GetPersistenceSchema() PersistenceSchema
}

// PersistenceSchema is the set of AttributeDef and ChannelDef values a Flow
// registers. Every attribute or channel used from WaitFor, Execute, or RPC must
// appear here.
type PersistenceSchema struct {
	Attributes []AttributeDef
	Channels   []ChannelDef
}
