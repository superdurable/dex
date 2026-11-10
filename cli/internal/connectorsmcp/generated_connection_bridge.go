// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorsmcp

import (
	"fmt"
	"reflect"
	"unsafe"

	"github.com/superdurable/dex-connectors-library/sdkgo"
)

// readGeneratedConnection returns the operations and reference of a generated connector Connection, or an error
// when its fields differ from what the connector generator emits.
func readGeneratedConnection[OPERATIONS any](connection any, connectionName string) (OPERATIONS, sdkgo.ConnectionRef, error) {
	// Bridge until generated Connections export Operations and Reference.
	var operations OPERATIONS
	value := reflect.New(reflect.TypeOf(connection)).Elem()
	value.Set(reflect.ValueOf(connection))
	operationsField := value.FieldByName("operations")
	if !operationsField.IsValid() || operationsField.Type() != reflect.TypeFor[OPERATIONS]() {
		return operations, sdkgo.ConnectionRef{}, fmt.Errorf("%T has no operations field of type %v", connection, reflect.TypeFor[OPERATIONS]())
	}
	referenceField := value.FieldByName("reference")
	if !referenceField.IsValid() || referenceField.Type() != reflect.TypeFor[sdkgo.ConnectionRef]() {
		return operations, sdkgo.ConnectionRef{}, fmt.Errorf("%T has no reference field of type sdkgo.ConnectionRef", connection)
	}
	operationsValue := reflect.NewAt(operationsField.Type(), unsafe.Pointer(operationsField.UnsafeAddr())).Elem()
	if operationsValue.IsNil() {
		return operations, sdkgo.ConnectionRef{}, fmt.Errorf("%T has no operations", connection)
	}
	operations = operationsValue.Interface().(OPERATIONS)
	reference := reflect.NewAt(referenceField.Type(), unsafe.Pointer(referenceField.UnsafeAddr())).Elem().Interface().(sdkgo.ConnectionRef)
	if err := reference.Validate(); err != nil {
		return operations, sdkgo.ConnectionRef{}, fmt.Errorf("%T reference: %w", connection, err)
	}
	if reference.Name != connectionName {
		return operations, sdkgo.ConnectionRef{}, fmt.Errorf("%T reference names connection %q, not %q", connection, reference.Name, connectionName)
	}
	return operations, reference, nil
}
