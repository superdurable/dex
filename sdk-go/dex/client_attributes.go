// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"context"
	"fmt"
	"strings"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

// AttributeRead selects one registered Attribute or AttributeMap instance for Client.GetAttributes.
//
// Create an AttributeRead with ReadAttribute or ReadAttributeMapInstance.
// Each AttributeRead pairs a definition with a destination for its typed value.
// GetAttributes writes the destination only when the value is set and the whole call succeeds.
// An unset value leaves the destination unchanged.
// Applications cannot implement AttributeRead.
// See Client.GetAttributes for an example.
type AttributeRead interface {
	attributeReadDefinition() AttributeDef
	attributeReadInstance() string
	hasAttributeReadDestination() bool
	decodeAttributeRead(*dexpb.Value) (writeDestination func(), err error)
}

type attributeReadImpl[T any] struct {
	definition  AttributeDef
	instance    string
	destination *T
}

// ReadAttribute selects a registered Attribute and the destination for its value.
//
// Pass the result to Client.GetAttributes.
// When the Attribute is set and the call succeeds, GetAttributes stores the decoded value in destination.
// When the Attribute is not set, destination keeps its previous value.
// Use AttributeReadResult.IsSet to tell an unset Attribute from a stored zero value.
// See Client.GetAttributes for an example.
//
// Type parameter T is the Attribute value type.
// It must match the T of the Attribute definition registered in the Client's Registry.
//
// Parameters:
//   - attribute is an Attribute definition registered by a Flow in the Client's Registry.
//   - destination points to the variable that receives the value. It must not be nil.
//
// Returns an AttributeRead for Client.GetAttributes.
// ReadAttribute does not validate its inputs or contact the server.
// GetAttributes reports invalid inputs.
func ReadAttribute[T any](attribute Attribute[T], destination *T) AttributeRead {
	return attributeReadImpl[T]{definition: attribute, destination: destination}
}

// ReadAttributeMapInstance selects one registered AttributeMap instance and the destination for its value.
//
// Pass the result to Client.GetAttributes.
// When the instance is set and the call succeeds, GetAttributes stores the decoded value in destination.
// When the instance is not set, destination keeps its previous value.
// Use AttributeReadResult.IsMapInstanceSet to tell an unset instance from a stored zero value.
// See Client.GetAttributes for an example.
//
// Type parameter T is the AttributeMap value type.
// It must match the T of the AttributeMap definition registered in the Client's Registry.
//
// Parameters:
//   - attributeMap is an AttributeMap definition registered by a Flow in the Client's Registry.
//   - instance is the map instance key. It must be non-empty and must not contain '/'.
//   - destination points to the variable that receives the value. It must not be nil.
//
// Returns an AttributeRead for Client.GetAttributes.
// ReadAttributeMapInstance does not validate its inputs or contact the server.
// GetAttributes reports invalid inputs.
func ReadAttributeMapInstance[T any](
	attributeMap AttributeMap[T],
	instance string,
	destination *T,
) AttributeRead {
	return attributeReadImpl[T]{definition: attributeMap, instance: instance, destination: destination}
}

func (read attributeReadImpl[T]) attributeReadDefinition() AttributeDef {
	return read.definition
}

func (read attributeReadImpl[T]) attributeReadInstance() string {
	return read.instance
}

func (read attributeReadImpl[T]) hasAttributeReadDestination() bool {
	return read.destination != nil
}

func (read attributeReadImpl[T]) decodeAttributeRead(value *dexpb.Value) (func(), error) {
	var decoded T
	if err := decodeValue(value, &decoded); err != nil {
		return nil, err
	}
	return func() { *read.destination = decoded }, nil
}

// AttributeReadResult reports which reads of one Client.GetAttributes call found a set value.
//
// GetAttributes returns an AttributeReadResult with every successful call.
// Most callers ignore it and rely on the destinations.
// Use it when an unset value must be distinguished from a stored zero value.
// The zero value is valid and reports every Attribute and AttributeMap instance as not set.
// GetAttributes returns the zero value with every error.
// See Client.GetAttributes for an example.
type AttributeReadResult struct {
	setKeys map[string]struct{}
}

// IsSet reports whether the call read attribute and found a set value.
//
// It returns false when attribute was not set.
// It also returns false when attribute was not part of the call, is nil, or is an AttributeMap.
// Use IsMapInstanceSet for an AttributeMap instance.
//
// Parameters:
//   - attribute is the Attribute definition passed to ReadAttribute.
//
// Returns true only when GetAttributes stored this Attribute's value in its destination.
func (result AttributeReadResult) IsSet(attribute AttributeDef) bool {
	if nilInterface(attribute) || attribute.attributeIsMap() {
		return false
	}
	return result.hasSetKey(attribute.attributeName(), "", false)
}

// IsMapInstanceSet reports whether the call read one AttributeMap instance and found a set value.
//
// It returns false when the instance was not set.
// It also returns false when the instance was not part of the call, attributeMap is nil, or attributeMap is not an AttributeMap.
//
// Parameters:
//   - attributeMap is the AttributeMap definition passed to ReadAttributeMapInstance.
//   - instance is the map instance key passed to ReadAttributeMapInstance.
//
// Returns true only when GetAttributes stored this instance's value in its destination.
func (result AttributeReadResult) IsMapInstanceSet(attributeMap AttributeDef, instance string) bool {
	if nilInterface(attributeMap) || !attributeMap.attributeIsMap() {
		return false
	}
	return result.hasSetKey(attributeMap.attributeName(), instance, true)
}

func (result AttributeReadResult) hasSetKey(name string, instance string, isMap bool) bool {
	key, err := physicalName(name, instance, isMap)
	if err != nil {
		return false
	}
	_, found := result.setKeys[key]
	return found
}

// GetAttributes reads committed Attribute values from the current or latest Flow run.
//
// Use GetAttributes to read Flow state from outside the Flow without defining an RPC.
// Pass one AttributeRead per value, created with ReadAttribute or ReadAttributeMapInstance.
// The Client sends all keys in one server request and does not dispatch a Worker.
// The read targets the current run, or the latest run when no run is open.
// A closed run stays readable until it is purged.
// The call sees only committed state.
// It does not see writes staged by an RPC that is still running.
//
// The servers resolve HardDeleteOnNull values before they respond.
// The Client then resolves large-value references through LoadBlobs and decodes every value.
// GetAttributes writes destinations only after every value decodes.
// On error, no destination has been written.
// An unset Attribute or AttributeMap instance leaves its destination unchanged.
// The returned AttributeReadResult reports which values were set.
// Most callers ignore it.
//
// A definition from any Flow in the Client's Registry is accepted.
// The server does not check that the target Flow's type declares it.
// A same-named Attribute of another type in the target Flow fails to decode.
//
//	type Note struct {
//		Text string `json:"text"`
//	}
//	orderStatus := dex.DefineAttribute[string]("order-status")
//	notes := dex.DefineAttributeMap[Note]("notes")
//
//	var status string
//	var note Note
//	result, err := client.GetAttributes(ctx, "order-123",
//		dex.ReadAttribute(orderStatus, &status),
//		dex.ReadAttributeMapInstance(notes, "n1", &note),
//	)
//	var notFound *dex.FlowNotFoundError
//	if errors.As(err, &notFound) {
//		return fmt.Errorf("order %q does not exist", notFound.FlowID)
//	}
//	if err != nil {
//		return err
//	}
//	if !result.IsSet(orderStatus) {
//		status = "new"
//	}
//	if !result.IsMapInstanceSet(notes, "n1") {
//		note = Note{Text: "no note yet"}
//	}
//
// Parameters:
//   - ctx controls cancellation and the deadline of the server request and LoadBlobs calls.
//   - flowID is the Flow ID. It must be non-empty.
//   - reads lists at least one AttributeRead. Each must be non-nil and have a non-nil destination.
//     Two reads of the same Attribute or AttributeMap instance are rejected.
//
// Returns an AttributeReadResult and nil after every destination of a set value has been written.
// Returns the zero AttributeReadResult and an error otherwise:
//   - *FlowDefinitionError when a definition is not registered in the Client's Registry,
//     or its value type differs from the registered definition.
//   - *FlowNotFoundError when the Flow does not exist or its latest run is being purged.
//   - *ValueMappingError when a stored value cannot be decoded into T.
//   - *ServiceError, or a concrete service error wrapping it, for other server failures.
//   - A plain error for an empty flowID, no reads, a nil read or destination, an invalid instance,
//     a duplicate read, an invalid server response, a LoadBlobs failure, or a context error.
//
// Every error names the Flow ID and the affected Attribute names.
// Match error types with errors.As.
func (client *Client) GetAttributes(
	ctx context.Context,
	flowID string,
	reads ...AttributeRead,
) (AttributeReadResult, error) {
	keys, err := client.validateAttributeReads(ctx, flowID, reads)
	if err != nil {
		return AttributeReadResult{}, err
	}
	values, err := client.fetchAttributeValues(ctx, flowID, keys)
	if err != nil {
		return AttributeReadResult{}, err
	}
	writeDestinations := make([]func(), 0, len(values))
	setKeys := make(map[string]struct{}, len(values))
	for index, read := range reads {
		value, found := values[keys[index]]
		if !found {
			continue
		}
		writeDestination, err := read.decodeAttributeRead(value)
		if err != nil {
			return AttributeReadResult{}, fmt.Errorf("dex: GetAttributes Flow %q Attribute %q: %w",
				flowID, keys[index], err)
		}
		writeDestinations = append(writeDestinations, writeDestination)
		setKeys[keys[index]] = struct{}{}
	}
	for _, writeDestination := range writeDestinations {
		writeDestination()
	}
	return AttributeReadResult{setKeys: setKeys}, nil
}

// validateAttributeReads returns the physical key of each read, in read order,
// or an error for the first invalid read.
func (client *Client) validateAttributeReads(
	ctx context.Context,
	flowID string,
	reads []AttributeRead,
) ([]string, error) {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return nil, err
	}
	if len(reads) == 0 {
		return nil, fmt.Errorf("dex: GetAttributes Flow %q requires at least one Attribute read", flowID)
	}
	keys := make([]string, 0, len(reads))
	seen := make(map[string]struct{}, len(reads))
	for _, read := range reads {
		if nilInterface(read) {
			return nil, fmt.Errorf("dex: GetAttributes Flow %q has a nil Attribute read", flowID)
		}
		definition := read.attributeReadDefinition()
		registered, err := client.registry.resolveAttribute(definition, definition.attributeIsMap())
		if err != nil {
			return nil, fmt.Errorf("dex: GetAttributes Flow %q: %w", flowID, err)
		}
		if registered.valueType != definition.attributeValueType() {
			return nil, fmt.Errorf("dex: GetAttributes Flow %q: %w", flowID, newFlowDefinitionError(
				"",
				fmt.Sprintf("attribute %q", registered.name),
				fmt.Errorf("value type %s does not match registered value type %s",
					definition.attributeValueType(), registered.valueType),
			))
		}
		if !read.hasAttributeReadDestination() {
			return nil, fmt.Errorf("dex: GetAttributes Flow %q Attribute %q has a nil destination",
				flowID, registered.name)
		}
		key, err := physicalName(registered.name, read.attributeReadInstance(), registered.isMap)
		if err != nil {
			return nil, fmt.Errorf("dex: GetAttributes Flow %q Attribute %q: %w", flowID, registered.name, err)
		}
		if _, found := seen[key]; found {
			return nil, fmt.Errorf("dex: GetAttributes Flow %q has duplicate Attribute read %q", flowID, key)
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys, nil
}

// fetchAttributeValues returns the hydrated value of every set key.
// A key absent from the returned map is not set.
func (client *Client) fetchAttributeValues(
	ctx context.Context,
	flowID string,
	keys []string,
) (map[string]*dexpb.Value, error) {
	joinedKeys := strings.Join(keys, ", ")
	response, err := client.service.GetAttributes(ctx, &dexpb.GetAttributesRequest{
		FlowId: flowID,
		Keys:   keys,
	})
	if err != nil {
		return nil, fmt.Errorf("dex: GetAttributes Flow %q Attributes %q: %w",
			flowID, joinedKeys, translateRPCError(err, "GetAttributes", flowID, flowTargetExisting))
	}
	if response == nil {
		return nil, fmt.Errorf("dex: GetAttributes Flow %q returned a nil response for Attributes %q",
			flowID, joinedKeys)
	}
	requested := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		requested[key] = struct{}{}
	}
	attributes := make(map[string]*dexpb.KV, len(response.Attributes))
	valuePointers := make([]**dexpb.Value, 0, len(response.Attributes))
	for _, attribute := range response.Attributes {
		if attribute == nil {
			return nil, fmt.Errorf("dex: GetAttributes Flow %q returned a nil Attribute", flowID)
		}
		if _, found := requested[attribute.Key]; !found {
			return nil, fmt.Errorf("dex: GetAttributes Flow %q returned unexpected Attribute %q",
				flowID, attribute.Key)
		}
		if _, found := attributes[attribute.Key]; found {
			return nil, fmt.Errorf("dex: GetAttributes Flow %q returned duplicate Attribute %q",
				flowID, attribute.Key)
		}
		if attribute.Value == nil {
			return nil, fmt.Errorf("dex: GetAttributes Flow %q returned Attribute %q without a value",
				flowID, attribute.Key)
		}
		attributes[attribute.Key] = attribute
		valuePointers = append(valuePointers, &attribute.Value)
	}
	if err := client.hydrateValues(ctx, flowID, valuePointers); err != nil {
		return nil, fmt.Errorf("dex: GetAttributes Flow %q Attributes %q: %w", flowID, joinedKeys, err)
	}
	values := make(map[string]*dexpb.Value, len(attributes))
	for key, attribute := range attributes {
		values[key] = attribute.Value
	}
	return values, nil
}
