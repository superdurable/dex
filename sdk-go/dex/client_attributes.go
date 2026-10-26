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
	"reflect"
	"strings"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

// AttributeRead selects a registered Attribute or AttributeMap instance for Client.GetAttributes.
//
// Create reads with ReadAttribute or ReadAttributeMapInstance. Each read holds a destination
// for one optional typed value. GetAttributes sets that destination to nil when the value
// has not been set. Applications cannot implement AttributeRead.
type AttributeRead interface {
	attributeReadDefinition() AttributeDef
	attributeReadInstance() string
	attributeReadDestinationValid() bool
	decodeAttributeRead(*dexpb.Value) error
}

type attributeReadImpl[T any] struct {
	definition  AttributeDef
	instance    string
	destination **T
}

// ReadAttribute selects a registered Attribute and a destination for its optional value.
//
// Pass the result to Client.GetAttributes. destination must point to a *T variable.
// After a successful read, nil means the Attribute is not set; a non-nil pointer
// can hold T's zero value. GetAttributes reports a nil destination as an error.
//
//	var status *string
//	orderStatus := dex.DefineAttribute[string]("order-status")
//	err := client.GetAttributes(ctx, "order-123", dex.ReadAttribute(orderStatus, &status))
func ReadAttribute[T any](attribute Attribute[T], destination **T) AttributeRead {
	return attributeReadImpl[T]{definition: attribute, destination: destination}
}

// ReadAttributeMapInstance selects one registered AttributeMap instance and its optional value.
//
// Pass the result to Client.GetAttributes. instance must be non-empty and contain no slash.
// destination must point to a *T variable. After a successful read, nil means the
// instance is not set; a non-nil pointer can hold T's zero value.
//
//	var quantity *int
//	orderQuantities := dex.DefineAttributeMap[int]("order-quantities")
//	err := client.GetAttributes(ctx, "order-123",
//		dex.ReadAttributeMapInstance(orderQuantities, "line-1", &quantity))
func ReadAttributeMapInstance[T any](
	attribute AttributeMap[T],
	instance string,
	destination **T,
) AttributeRead {
	return attributeReadImpl[T]{definition: attribute, instance: instance, destination: destination}
}

func (read attributeReadImpl[T]) attributeReadDefinition() AttributeDef {
	return read.definition
}

func (read attributeReadImpl[T]) attributeReadInstance() string {
	return read.instance
}

func (read attributeReadImpl[T]) attributeReadDestinationValid() bool {
	return read.destination != nil
}

func (read attributeReadImpl[T]) decodeAttributeRead(value *dexpb.Value) error {
	if value == nil {
		*read.destination = nil
		return nil
	}
	var decoded T
	if err := decodeValue(value, &decoded); err != nil {
		return err
	}
	*read.destination = &decoded
	return nil
}

// GetAttributes reads committed Attribute values from the current or latest Flow run.
//
// Pass one or more reads created with ReadAttribute or ReadAttributeMapInstance. The
// Client sends their keys in one server request and uses LoadBlobs when returned
// values need hydration. It does not dispatch a Worker. A closed run can be read
// until it is purged. Unlike a read inside an RPC, this call does not share that
// RPC's state snapshot or staged writes. Each destination is nil for an unset
// Attribute or AttributeMap instance, even when its type has a zero value.
//
// ctx controls cancellation; flowID must be non-empty. Every read must use a
// definition in the Client's Registry and a non-nil destination. Duplicate reads
// of the same Attribute or AttributeMap instance are rejected. A missing Flow or
// purged run returns FlowNotFoundError when the server reports it as missing.
// Validation, decoding, hydration, context, transport, and server failures are
// returned as errors naming the Flow ID and affected Attribute names. Destinations
// should be inspected only after this method returns nil.
//
//	var status *string
//	var quantity *int
//	orderStatus := dex.DefineAttribute[string]("order-status")
//	orderQuantities := dex.DefineAttributeMap[int]("order-quantities")
//	err := client.GetAttributes(ctx, "order-123",
//		dex.ReadAttribute(orderStatus, &status),
//		dex.ReadAttributeMapInstance(orderQuantities, "line-1", &quantity))
func (client *Client) GetAttributes(
	ctx context.Context,
	flowID string,
	reads ...AttributeRead,
) error {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return err
	}
	if len(reads) == 0 {
		return fmt.Errorf("dex: GetAttributes Flow %q requires at least one Attribute read", flowID)
	}
	keys := make([]string, 0, len(reads))
	seen := make(map[string]struct{}, len(reads))
	for _, read := range reads {
		if read == nil {
			return fmt.Errorf("dex: GetAttributes Flow %q has a nil Attribute read", flowID)
		}
		definition := read.attributeReadDefinition()
		name := definition.attributeName()
		if !read.attributeReadDestinationValid() {
			return fmt.Errorf("dex: GetAttributes Flow %q Attribute %q has a nil destination", flowID, name)
		}
		if !client.isRegisteredAttributeDefinition(definition) {
			return fmt.Errorf("dex: GetAttributes Flow %q Attribute %q is not registered", flowID, name)
		}
		key, err := physicalName(name, read.attributeReadInstance(), definition.attributeIsMap())
		if err != nil {
			return fmt.Errorf("dex: GetAttributes Flow %q Attribute %q: %w", flowID, name, err)
		}
		if _, found := seen[key]; found {
			return fmt.Errorf("dex: GetAttributes Flow %q has duplicate Attribute read %q", flowID, key)
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	response, err := client.service.GetAttributes(ctx, &dexpb.GetAttributesRequest{
		FlowId: flowID,
		Keys:   keys,
	})
	if err != nil {
		return fmt.Errorf("dex: GetAttributes Flow %q Attributes %q: %w",
			flowID, strings.Join(keys, ", "),
			translateRPCError(err, "GetAttributes", flowID, flowTargetExisting))
	}
	if response == nil {
		return fmt.Errorf("dex: GetAttributes Flow %q returned a nil response for Attributes %q",
			flowID, strings.Join(keys, ", "))
	}
	values := make(map[string]*dexpb.KV, len(response.Attributes))
	valuePointers := make([]**dexpb.Value, 0, len(response.Attributes))
	for _, attribute := range response.Attributes {
		if attribute == nil {
			return fmt.Errorf("dex: GetAttributes Flow %q returned a nil Attribute", flowID)
		}
		if _, found := seen[attribute.Key]; !found {
			return fmt.Errorf("dex: GetAttributes Flow %q returned unexpected Attribute %q", flowID, attribute.Key)
		}
		if _, found := values[attribute.Key]; found {
			return fmt.Errorf("dex: GetAttributes Flow %q returned duplicate Attribute %q", flowID, attribute.Key)
		}
		if attribute.Value == nil {
			return fmt.Errorf("dex: GetAttributes Flow %q returned Attribute %q without a value", flowID, attribute.Key)
		}
		values[attribute.Key] = attribute
		valuePointers = append(valuePointers, &attribute.Value)
	}
	if err := client.hydrateValues(ctx, flowID, valuePointers); err != nil {
		return fmt.Errorf("dex: GetAttributes Flow %q Attributes %q: %w",
			flowID, strings.Join(keys, ", "), err)
	}
	for index, read := range reads {
		var value *dexpb.Value
		if attribute := values[keys[index]]; attribute != nil {
			value = attribute.Value
		}
		if err := read.decodeAttributeRead(value); err != nil {
			return fmt.Errorf("dex: GetAttributes Flow %q Attribute %q: %w", flowID, keys[index], err)
		}
	}
	return nil
}

func (client *Client) isRegisteredAttributeDefinition(definition AttributeDef) bool {
	for _, flow := range client.registry.flows {
		registered, found := flow.attributes[definition.attributeName()]
		if found && reflect.DeepEqual(registered.def, definition) {
			return true
		}
	}
	return false
}
