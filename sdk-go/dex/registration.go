// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"fmt"
	"reflect"
	"strings"
)

// Registry stores immutable Flow definitions shared by Client and Worker.
type Registry struct {
	flows map[string]*registeredFlow
}

type registeredFlow struct {
	flow         Flow
	flowType     string
	startingStep *registeredStep
	steps        map[string]*registeredStep
	rpcs         map[string]*registeredRPC
	attributes   map[string]registeredAttribute
	channels     map[string]registeredChannel
}

type registeredStep struct {
	handler     StepDef
	stepType    string
	inputType   reflect.Type
	options     *StepOptions
	starting    bool
	skipWaitFor bool
}

type registeredAttribute struct {
	def   AttributeDef
	name  string
	index *AttributeIndex
	isMap bool
}

type registeredChannel struct {
	def   ChannelDef
	name  string
	isMap bool
}

// NewRegistry validates and assembles Flow definitions atomically.
func NewRegistry(flows []Flow) (*Registry, error) {
	assembled := &Registry{
		flows: make(map[string]*registeredFlow, len(flows)),
	}
	indexTypes := make(map[string]IndexType)
	for index, flow := range flows {
		if nilInterface(flow) {
			return nil, newFlowDefinitionError(
				"",
				"",
				fmt.Errorf("flow at index %d is nil", index),
			)
		}
		flowType := GetFinalFlowType(flow)
		registered, err := assembled.registerFlow(flow, indexTypes)
		if err != nil {
			return nil, newFlowDefinitionError(flowType, "", err)
		}
		if _, found := assembled.flows[registered.flowType]; found {
			return nil, newFlowDefinitionError(
				registered.flowType,
				"",
				fmt.Errorf("duplicate flow type %q", registered.flowType),
			)
		}
		assembled.flows[registered.flowType] = registered
	}
	return assembled, nil
}

func (registry *Registry) registerFlow(
	flow Flow,
	indexTypes map[string]IndexType,
) (*registeredFlow, error) {
	flowType := GetFinalFlowType(flow)
	if flowType == "" {
		return nil, fmt.Errorf("dex: flow must use a named package type")
	}
	registered := &registeredFlow{
		flow:       flow,
		flowType:   flowType,
		steps:      make(map[string]*registeredStep),
		rpcs:       make(map[string]*registeredRPC),
		attributes: make(map[string]registeredAttribute),
		channels:   make(map[string]registeredChannel),
	}
	if err := registered.registerPersistence(
		flow.GetPersistenceSchema(),
		indexTypes,
	); err != nil {
		return nil, fmt.Errorf("dex: flow %q: %w", flowType, err)
	}
	if err := registered.registerSteps(flow.GetSteps()); err != nil {
		return nil, fmt.Errorf("dex: flow %q: %w", flowType, err)
	}
	rpcs, err := discoverRPCs(flow)
	if err != nil {
		return nil, fmt.Errorf("dex: flow %q: %w", flowType, err)
	}
	registered.rpcs = rpcs
	return registered, nil
}

func (flow *registeredFlow) registerPersistence(
	schema PersistenceSchema,
	indexTypes map[string]IndexType,
) error {
	for index, definition := range schema.Attributes {
		if nilInterface(definition) {
			return fmt.Errorf("attribute at index %d is nil", index)
		}
		if err := flow.registerAttribute(definition, indexTypes); err != nil {
			return err
		}
	}
	for index, definition := range schema.Channels {
		if nilInterface(definition) {
			return fmt.Errorf("channel at index %d is nil", index)
		}
		if err := flow.registerChannel(definition); err != nil {
			return err
		}
	}
	return nil
}

func (flow *registeredFlow) registerAttribute(
	definition AttributeDef,
	indexTypes map[string]IndexType,
) error {
	name := definition.attributeName()
	if name == "" {
		return fmt.Errorf("attribute name must not be empty")
	}
	if _, found := flow.attributes[name]; found {
		return fmt.Errorf("duplicate attribute %q", name)
	}
	index := definition.attributeIndex()
	isMap := definition.attributeIsMap()
	if index != nil {
		if _, err := mapIndexType(index.Type); err != nil {
			return fmt.Errorf("attribute %q: %w", name, err)
		}
		indexKey := effectiveIndexKey(name, index, isMap)
		if indexKey != "" {
			if existing, found := indexTypes[indexKey]; found &&
				existing != index.Type {
				return fmt.Errorf(
					"index key %q has conflicting types %d and %d",
					indexKey,
					existing,
					index.Type,
				)
			}
			indexTypes[indexKey] = index.Type
		}
	}
	flow.attributes[name] = registeredAttribute{
		def:   definition,
		name:  name,
		index: index,
		isMap: isMap,
	}
	return nil
}

func (flow *registeredFlow) registerChannel(
	definition ChannelDef,
) error {
	name := definition.channelName()
	if name == "" {
		return fmt.Errorf("channel name must not be empty")
	}
	if _, found := flow.channels[name]; found {
		return fmt.Errorf("duplicate channel %q", name)
	}
	flow.channels[name] = registeredChannel{
		def:   definition,
		name:  name,
		isMap: definition.channelIsMap(),
	}
	return nil
}

func (flow *registeredFlow) registerSteps(definitions []StepDef) error {
	for index, definition := range definitions {
		if err := flow.registerStep(definition, index); err != nil {
			return err
		}
	}
	// Validate in GetSteps order so the first illegal step is reported stably.
	for _, definition := range definitions {
		step := flow.steps[definition.stepType()]
		if err := flow.validateStepOptions(step.options, step.inputType); err != nil {
			return fmt.Errorf("step %q options: %w", step.stepType, err)
		}
	}
	return nil
}

func (flow *registeredFlow) registerStep(
	definition StepDef,
	index int,
) error {
	if definition == nil || nilInterface(definition.stepValue()) {
		return fmt.Errorf("step at index %d is nil", index)
	}
	stepType := definition.stepType()
	if stepType == "" {
		return fmt.Errorf("step at index %d has an empty type", index)
	}
	if _, found := flow.steps[stepType]; found {
		return fmt.Errorf("duplicate step type %q", stepType)
	}
	registered := &registeredStep{
		handler:     definition,
		stepType:    stepType,
		inputType:   definition.stepInputType(),
		options:     definition.stepOptions(),
		starting:    definition.isStarting(),
		skipWaitFor: definition.skipWaitFor(),
	}
	if definition.isStarting() {
		if flow.startingStep != nil {
			return fmt.Errorf(
				"multiple starting steps %q and %q",
				flow.startingStep.stepType,
				stepType,
			)
		}
		flow.startingStep = registered
	}
	flow.steps[stepType] = registered
	return nil
}

func (flow *registeredFlow) validateStepOptions(
	options *StepOptions,
	inputType reflect.Type,
) error {
	if err := flow.doValidateStepOptions(
		options,
		inputType,
		make(map[*StepOptions]bool),
	); err != nil {
		return err
	}
	if _, err := mapStepOptions(options); err != nil {
		return err
	}
	return nil
}

func (flow *registeredFlow) doValidateStepOptions(
	options *StepOptions,
	inputType reflect.Type,
	active map[*StepOptions]bool,
) error {
	if options == nil {
		return nil
	}
	if active[options] {
		return fmt.Errorf("step options contain a cycle")
	}
	active[options] = true
	defer delete(active, options)

	if err := flow.validateAttributeLocks(
		options.WaitForLockAttributes,
	); err != nil {
		return fmt.Errorf("WaitFor locks: %w", err)
	}
	if err := flow.validateAttributeLocks(
		options.ExecuteLockAttributes,
	); err != nil {
		return fmt.Errorf("Execute locks: %w", err)
	}
	if options.ExecuteFailure == nil {
		return nil
	}
	target, err := flow.resolveStepReference(options.ExecuteFailure.step)
	if err != nil {
		return fmt.Errorf("Execute failure target: %w", err)
	}
	if target.inputType != inputType {
		return fmt.Errorf(
			"Execute failure target %q input %s does not match %s",
			target.stepType,
			target.inputType,
			inputType,
		)
	}
	return flow.doValidateStepOptions(
		options.ExecuteFailure.options,
		target.inputType,
		active,
	)
}

func (flow *registeredFlow) validateAttributeLocks(
	locks []AttributeLock,
) error {
	seen := make(map[string]struct{}, len(locks))
	for _, lock := range locks {
		concrete, ok := lock.(attributeLock)
		if !ok {
			return fmt.Errorf("invalid attribute lock %T", lock)
		}
		attribute, found := flow.attributes[concrete.name]
		if !found {
			return fmt.Errorf("attribute %q is not declared", concrete.name)
		}
		if attribute.isMap != concrete.isMap {
			return fmt.Errorf(
				"attribute %q static/map kind does not match its lock",
				concrete.name,
			)
		}
		name, err := physicalName(
			concrete.name,
			concrete.instance,
			concrete.isMap,
		)
		if err != nil {
			return err
		}
		if _, found := seen[name]; found {
			return fmt.Errorf("duplicate attribute lock %q", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func (flow *registeredFlow) resolveMovement(
	movement StepMovement,
) (*registeredStep, error) {
	target, err := flow.resolveStepReference(movement.step)
	if err != nil {
		return nil, err
	}
	if !assignableValue(movement.input, target.inputType) {
		return nil, fmt.Errorf(
			"movement input %T is not assignable to step %q input %s",
			movement.input,
			target.stepType,
			target.inputType,
		)
	}
	if err := flow.validateStepOptions(
		movement.options,
		target.inputType,
	); err != nil {
		return nil, fmt.Errorf(
			"movement to step %q options: %w",
			target.stepType,
			err,
		)
	}
	return target, nil
}

func (flow *registeredFlow) resolveStepReference(
	reference StepDef,
) (*registeredStep, error) {
	if reference == nil || nilInterface(reference.stepValue()) {
		return nil, fmt.Errorf("step reference is nil")
	}
	stepType := reference.stepType()
	if stepType == "" {
		return nil, fmt.Errorf("step reference type must not be empty")
	}
	target, found := flow.steps[stepType]
	if !found {
		return nil, fmt.Errorf("step %q is not registered", stepType)
	}
	if reference.stepInputType() != target.inputType {
		return nil, fmt.Errorf(
			"step %q reference input %s does not match registered input %s",
			stepType,
			reference.stepInputType(),
			target.inputType,
		)
	}
	return target, nil
}

func (flow *registeredFlow) resolveChannels(
	definitions []ChannelDef,
) ([]registeredChannel, error) {
	if len(definitions) == 0 {
		return nil, fmt.Errorf("channel references must not be empty")
	}
	resolved := make([]registeredChannel, 0, len(definitions))
	seen := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if nilInterface(definition) {
			return nil, fmt.Errorf("channel reference is nil")
		}
		name := definition.channelName()
		channel, found := flow.channels[name]
		if !found {
			return nil, fmt.Errorf("channel %q is not declared", name)
		}
		if channel.isMap != definition.channelIsMap() {
			return nil, fmt.Errorf(
				"channel %q static/map kind does not match its definition",
				name,
			)
		}
		if _, found := seen[name]; found {
			return nil, fmt.Errorf(
				"duplicate channel reference %q",
				name,
			)
		}
		seen[name] = struct{}{}
		resolved = append(resolved, channel)
	}
	return resolved, nil
}

func (registry *Registry) lookupFlow(
	flowType string,
) (*registeredFlow, bool) {
	flow, found := registry.flows[flowType]
	return flow, found
}

func (registry *Registry) resolveFlow(reference Flow) (*registeredFlow, error) {
	if nilInterface(reference) {
		return nil, fmt.Errorf("dex: flow is nil")
	}
	flowType := GetFinalFlowType(reference)
	if flowType == "" {
		return nil, fmt.Errorf("dex: flow must use a named package type")
	}
	registered, found := registry.lookupFlow(flowType)
	if !found {
		return nil, newFlowDefinitionError(
			flowType,
			"",
			fmt.Errorf("flow is not registered"),
		)
	}
	if reflect.TypeOf(reference) != reflect.TypeOf(registered.flow) {
		return nil, newFlowDefinitionError(
			flowType,
			"",
			fmt.Errorf(
				"type %s does not match registered type %s",
				reflect.TypeOf(reference),
				reflect.TypeOf(registered.flow),
			),
		)
	}
	return registered, nil
}

// GetFinalFlowType returns the override or the default package-qualified Go type.
func GetFinalFlowType(flow Flow) string {
	if flowType := flow.GetFlowType(); flowType != "" {
		return flowType
	}
	return getSimpleTypeNameFromReflect(flow)
}

// GetFinalStepType returns the override or the default package-qualified Go type.
func GetFinalStepType[IN any](step Step[IN]) string {
	if stepType := step.GetStepType(); stepType != "" {
		return stepType
	}
	return getSimpleTypeNameFromReflect(step)
}

func getSimpleTypeNameFromReflect(value any) string {
	valueType := reflect.TypeOf(value)
	return strings.TrimLeft(valueType.String(), "*")
}

func (registry *Registry) resolveRPC(reference any) (*registeredFlow, *registeredRPC, error) {
	rpcName, err := rpcMethodName(reference)
	if err != nil {
		return nil, nil, err
	}
	referenceType := reflect.TypeOf(reference)
	result := reflect.Zero(referenceType.Out(0).Elem()).Interface().(rpcResult)
	inputType := referenceType.In(1)
	outputType := result.rpcOutputType()
	identity, err := rpcMethodIdentity(reference)
	if err != nil {
		return nil, nil, err
	}
	for _, flow := range registry.flows {
		registered, found := flow.lookupRPC(rpcName)
		if found && registered.identity == identity &&
			registered.input == inputType && registered.output == outputType {
			return flow, registered, nil
		}
	}
	return nil, nil, newFlowDefinitionError(
		"",
		fmt.Sprintf("rpc %q", rpcName),
		fmt.Errorf(
			"input %s and output %s are not registered",
			inputType,
			outputType,
		),
	)
}

func (registry *Registry) resolveAttribute(
	reference AttributeDef,
	expectMap bool,
) (registeredAttribute, error) {
	if nilInterface(reference) {
		return registeredAttribute{}, fmt.Errorf("dex: attribute definition is nil")
	}
	name := reference.attributeName()
	if name == "" {
		return registeredAttribute{}, fmt.Errorf("dex: attribute name must not be empty")
	}
	if reference.attributeIsMap() != expectMap {
		return registeredAttribute{}, fmt.Errorf(
			"dex: attribute %q has the wrong static/map kind",
			name,
		)
	}
	for _, flow := range registry.flows {
		registered, found := flow.attributes[name]
		if found && registered.isMap == expectMap &&
			reflect.DeepEqual(registered.index, reference.attributeIndex()) {
			return registered, nil
		}
	}
	return registeredAttribute{}, newFlowDefinitionError(
		"",
		fmt.Sprintf("attribute %q", name),
		fmt.Errorf("attribute is not registered"),
	)
}

func (registry *Registry) resolveChannel(
	reference ChannelDef,
	expectMap bool,
) (registeredChannel, error) {
	if nilInterface(reference) {
		return registeredChannel{}, fmt.Errorf("dex: channel definition is nil")
	}
	name := reference.channelName()
	if name == "" {
		return registeredChannel{}, fmt.Errorf("dex: channel name must not be empty")
	}
	if reference.channelIsMap() != expectMap {
		return registeredChannel{}, fmt.Errorf(
			"dex: channel %q has the wrong static/map kind",
			name,
		)
	}
	for _, flow := range registry.flows {
		registered, found := flow.channels[name]
		if found && registered.isMap == expectMap {
			return registered, nil
		}
	}
	return registeredChannel{}, newFlowDefinitionError(
		"",
		fmt.Sprintf("channel %q", name),
		fmt.Errorf("channel is not registered"),
	)
}

func (flow *registeredFlow) lookupStep(
	stepType string,
) (*registeredStep, bool) {
	step, found := flow.steps[stepType]
	return step, found
}

func (flow *registeredFlow) lookupRPC(
	rpcName string,
) (*registeredRPC, bool) {
	rpc, found := flow.rpcs[rpcName]
	return rpc, found
}

func effectiveIndexKey(
	name string,
	index *AttributeIndex,
	isMap bool,
) string {
	if index.IndexKey != "" {
		return index.IndexKey
	}
	if isMap {
		return ""
	}
	return name
}

func assignableValue(value any, targetType reflect.Type) bool {
	if value == nil {
		return isNilableType(targetType)
	}
	return reflect.TypeOf(value).AssignableTo(targetType)
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	return reflected.IsValid() && isNilValue(reflected)
}
