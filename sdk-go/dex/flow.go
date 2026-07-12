package dex

import (
	"reflect"
	"strings"
)

// Flow defines a workflow.
type Flow interface {
	GetSteps() []StepDef
	GetPersistenceSchema() PersistenceSchema
}

// StepDef wraps a Step[IN] in a type-erased container.
type StepDef struct {
	Step       stepCommon
	IsStarting bool
}

// ChannelDef registers a channel with the flow schema.
type ChannelDef struct {
	Name      string
	IsDynamic bool
}

// StartingStep creates a StepDef that can be the entry point of the flow.
func StartingStep[IN any](step Step[IN]) StepDef {
	return StepDef{Step: step, IsStarting: true}
}

// NonStartingStep creates a StepDef that is not an entry point.
func NonStartingStep[IN any](step Step[IN]) StepDef {
	return StepDef{Step: step, IsStarting: false}
}

// DefineChannel registers a static channel with the flow schema.
func DefineChannel[T any](ch Channel[T]) ChannelDef {
	return ChannelDef{Name: ch.Name, IsDynamic: false}
}

// DefineDynamicChannel registers a dynamic channel family with the flow schema.
func DefineDynamicChannel[T any](ch DynamicChannel[T]) ChannelDef {
	return ChannelDef{Name: ch.Prefix, IsDynamic: true}
}

// GetFinalFlowType returns the flow type string used for registration.
func GetFinalFlowType(flow Flow) string {
	return flowTypeFromReflect(flow)
}

func flowTypeFromReflect(value any) string {
	rt := reflect.TypeOf(value)
	return strings.TrimLeft(rt.String(), "*")
}

// DefaultFlowType provides a default empty flow type.
type DefaultFlowType struct{}

// EmptyChannels provides a default empty PersistenceSchema.
type EmptyChannels struct{}

func (EmptyChannels) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{}
}

// FlowDefaults combines DefaultFlowType and EmptyChannels.
type FlowDefaults struct {
	DefaultFlowType
	EmptyChannels
}
