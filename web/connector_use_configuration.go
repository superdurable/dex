// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/superdurable/dex/web/api"
)

func validateConnectorUseConfiguration(
	configuration map[string]json.RawMessage,
	configurationUI api.V2ConnectorConfigurationUI,
) error {
	allowedPointers := make(map[string]bool)
	for _, unit := range configurationUI.Units {
		for _, binding := range unit.Bindings {
			allowedPointers[binding.JSONPointer] = true
		}
	}
	for key, rawValue := range configuration {
		var value any
		if err := json.Unmarshal(rawValue, &value); err != nil {
			return fmt.Errorf("Connector configuration value is invalid")
		}
		if err := validateConnectorUseValue(value, "/"+escapeConnectorJSONPointer(key), allowedPointers); err != nil {
			return err
		}
	}
	return nil
}

func validateConnectorUseValue(value any, pointer string, allowedPointers map[string]bool) error {
	if object, ok := value.(map[string]any); ok {
		if !connectorUsePointerAllowed(pointer, allowedPointers) {
			return fmt.Errorf("Connector configuration path %q is not declared by the Flow", pointer)
		}
		for key, nested := range object {
			if err := validateConnectorUseValue(nested, pointer+"/"+escapeConnectorJSONPointer(key), allowedPointers); err != nil {
				return err
			}
		}
		return nil
	}
	if !allowedPointers[pointer] {
		return fmt.Errorf("Connector configuration path %q is not declared by the Flow", pointer)
	}
	return nil
}

func connectorUsePointerAllowed(pointer string, allowedPointers map[string]bool) bool {
	if allowedPointers[pointer] {
		return true
	}
	prefix := pointer + "/"
	for allowed := range allowedPointers {
		if strings.HasPrefix(allowed, prefix) {
			return true
		}
	}
	return false
}

func escapeConnectorJSONPointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
