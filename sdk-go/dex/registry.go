package dex

import (
	"fmt"
	"reflect"
)

type flowRegistration struct {
	flow          Flow
	schema        PersistenceSchema
	steps         map[string]stepCommon
	startingSteps []string
}

// RegistryOptions configures Registry construction.
type RegistryOptions struct {
	// ObjectCodec encodes/decodes step inputs, state, and channel values.
	// Nil defaults to DefaultObjectCodec().
	ObjectCodec ObjectCodec
}

// Registry stores flows keyed by flow type string and their steps.
type Registry struct {
	flows       map[string]*flowRegistration
	objectCodec ObjectCodec
}

// NewRegistry creates an empty Registry with DefaultObjectCodec.
func NewRegistry() *Registry {
	return NewRegistryWithOptions(RegistryOptions{})
}

// NewRegistryWithOptions creates a Registry with the given options.
func NewRegistryWithOptions(opts RegistryOptions) *Registry {
	codec := opts.ObjectCodec
	if codec == nil {
		codec = DefaultObjectCodec()
	}
	return &Registry{
		flows:       make(map[string]*flowRegistration),
		objectCodec: codec,
	}
}

// ObjectCodec returns the codec used for this registry's client and worker.
func (r *Registry) ObjectCodec() ObjectCodec {
	return r.objectCodec
}

// Register adds a Flow and all its steps to the registry.
func (r *Registry) Register(flow Flow) {
	flowType := GetFinalFlowType(flow)
	if _, exists := r.flows[flowType]; exists {
		panic(fmt.Sprintf("dex: duplicate flow type %q", flowType))
	}

	reg := flowRegistration{
		flow:   flow,
		schema: flow.GetPersistenceSchema(),
		steps:  make(map[string]stepCommon),
	}

	for _, stepDef := range flow.GetSteps() {
		step := stepDef.Step
		stepID := stepIDFromCommon(step)
		if _, exists := reg.steps[stepID]; exists {
			panic(fmt.Sprintf("dex: duplicate step ID %q in flow %q", stepID, flowType))
		}
		reg.steps[stepID] = step
		if stepDef.IsStarting {
			reg.startingSteps = append(reg.startingSteps, stepID)
		}
	}

	r.flows[flowType] = &reg

	for stepID, step := range reg.steps {
		validateStepProceedOptions(flowType, stepID, step, reg)
	}
}

func (r *Registry) getFlowRegistration(flowType string) (*flowRegistration, bool) {
	reg, ok := r.flows[flowType]
	return reg, ok
}

func (r *Registry) getStep(flowType, stepID string) (stepCommon, bool) {
	reg, ok := r.flows[flowType]
	if !ok {
		return nil, false
	}
	step, ok := reg.steps[stepID]
	return step, ok
}

func validateStepProceedOptions(flowType, stepID string, step stepCommon, reg flowRegistration) {
	opts := step.GetStepOptions()
	if opts == nil {
		return
	}
	if handler := opts.WaitForMethodProceedToAfterRetryExhausted; handler != nil {
		validateProceedHandler(
			flowType, stepID, "WaitForMethodProceedToAfterRetryExhausted", step, handler, reg,
		)
	}
	if handler := opts.ExecuteMethodProceedToAfterRetryExhausted; handler != nil {
		validateProceedHandler(
			flowType, stepID, "ExecuteMethodProceedToAfterRetryExhausted", step, handler, reg,
		)
	}
}

func validateProceedHandler(
	flowType, stepID, optionName string,
	step, handler stepCommon,
	reg flowRegistration,
) {
	handlerID := stepIDFromCommon(handler)
	if _, ok := reg.steps[handlerID]; !ok {
		panic(fmt.Sprintf(
			"dex: step %q %s handler %q not registered in flow %q",
			stepID, optionName, handlerID, flowType,
		))
	}
	failingType, err := stepInputReflectType(step)
	if err != nil {
		panic(fmt.Sprintf("dex: step %q in flow %q: %v", stepID, flowType, err))
	}
	handlerType, err := stepInputReflectType(handler)
	if err != nil {
		panic(fmt.Sprintf(
			"dex: step %q %s handler %q: %v",
			stepID, optionName, handlerID, err,
		))
	}
	if !proceedInputTypesCompatible(failingType, handlerType) {
		panic(fmt.Sprintf(
			"dex: step %q %s handler %q input type %s incompatible with step input type %s (flow %q)",
			stepID, optionName, handlerID, handlerType, failingType, flowType,
		))
	}
}

func proceedInputTypesCompatible(failing, handler reflect.Type) bool {
	if failing == handler {
		return true
	}
	if handler == nil {
		return false
	}
	// Step[any] handler accepts any failing-step input encoding.
	if handler.Kind() == reflect.Interface && handler.NumMethod() == 0 {
		return true
	}
	return false
}
