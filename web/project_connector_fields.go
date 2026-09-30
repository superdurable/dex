// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/superdurable/dex-connectors-library/sdkgo/projectconfig"
	"github.com/superdurable/dex/web/api"
)

func (setup *connectorSetup) handlePutProjectTriggerBinding(response http.ResponseWriter, request *http.Request) {
	_, identity, ok := setup.authorizeConnectionWrite(response, request)
	if !ok {
		return
	}
	manifest, _, err := setup.project.readManifest(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	triggerName, bindingName := request.PathValue("triggerName"), request.PathValue("bindingName")
	declared := false
	for _, connection := range manifest.Manifest.Connectors {
		if connection.ConnectorID == identity.ConnectorID && connection.ConnectionName == identity.ConnectionName {
			for _, binding := range connection.TriggerBindings {
				if binding.TriggerName == triggerName && binding.BindingName == bindingName {
					declared = true
				}
			}
		}
	}
	if !declared {
		api.WriteCodedError(response, 404, "CONNECTOR_TRIGGER_BINDING_UNSUPPORTED", "Trigger binding is not declared by the AppManifest")
		return
	}
	resolved, err := setup.resolveConnectorRelease(request.Context(), identity)
	if err != nil {
		api.WriteCodedError(response, 502, "CONNECTOR_RELEASE_UNAVAILABLE", "Verified connector release is unavailable")
		return
	}
	trigger, found := projectTriggerSchema(resolved.release.Manifest, triggerName)
	if !found || trigger.Configuration == nil {
		api.WriteCodedError(response, 409, "CONNECTOR_TRIGGER_SCHEMA_UNAVAILABLE", "The pinned connector release does not publish this trigger configuration schema")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 1<<20)
	var body connectorTriggerBindingWriteRequest
	if err = decodeStrictConnectorJSONReader(request.Body, &body); err != nil || body.Configuration == nil {
		api.WriteCodedError(response, 400, "CONNECTOR_REQUEST_INVALID", "Trigger configuration request is invalid")
		return
	}
	if err = validateProjectFields(trigger.Configuration.Fields, body.Configuration); err != nil {
		api.WriteCodedError(response, 400, "CONNECTOR_TRIGGER_CONFIGURATION_INVALID", err.Error())
		return
	}
	current, _, err := setup.project.readManifest(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	if current.Revision != manifest.Revision {
		writeProjectConfigurationError(response, projectconfig.ErrConflict)
		return
	}
	store := setup.requestStore(request)
	if err = store.putTriggerBinding(localConnectorTriggerBinding{ConnectorID: identity.ConnectorID, ConnectionName: identity.ConnectionName, TriggerName: triggerName, BindingName: bindingName, Configuration: body.Configuration}); err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	writeWebJSON(response, 200, map[string]any{"configurationRevision": store.state().ConfigurationRevision, "triggerName": triggerName, "bindingName": bindingName})
}

func projectTriggerSchema(manifest connectorReleaseManifest, name string) (connectorManifestTrigger, bool) {
	for _, trigger := range manifest.Spec.Triggers {
		if trigger.Name == name {
			return trigger, true
		}
	}
	return connectorManifestTrigger{}, false
}

// validateProjectFields validates ordinary fields and normalizes durations for strict Go application decoders.
func validateProjectFields(fields []connectorManifestField, values map[string]json.RawMessage) error {
	known := make(map[string]connectorManifestField, len(fields))
	for _, field := range fields {
		if field.Type == "secretString" {
			return errors.New("ordinary configuration cannot declare credential fields")
		}
		known[field.Name] = field
		if field.Required && field.Default == nil && len(values[field.Name]) == 0 {
			return errors.New("required configuration field is missing")
		}
	}
	for name, contents := range values {
		field, exists := known[name]
		if !exists {
			return errors.New("configuration contains an undeclared field")
		}
		decoder := json.NewDecoder(bytes.NewReader(contents))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil || value == nil {
			return errors.New("configuration field has invalid JSON")
		}
		switch field.Type {
		case "string", "url", "enum":
			text, ok := value.(string)
			if !ok || (field.Required && text == "") {
				return errors.New("configuration requires a string")
			}
			if field.Type == "enum" && !slices.Contains(field.Enum, text) {
				return errors.New("configuration choice is not declared")
			}
			if field.Type == "url" {
				target, err := url.Parse(text)
				if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil {
					return errors.New("configuration URL must be absolute HTTPS")
				}
			}
		case "duration":
			if text, ok := value.(string); ok {
				duration, err := time.ParseDuration(text)
				if err != nil {
					return errors.New("configuration duration is invalid")
				}
				encoded, err := json.Marshal(int64(duration))
				if err != nil {
					return err
				}
				values[name] = encoded
			} else if number, ok := value.(json.Number); ok {
				if _, err := number.Int64(); err != nil {
					return errors.New("configuration duration must be integer nanoseconds")
				}
			} else {
				return errors.New("configuration duration is invalid")
			}
		case "integer":
			number, ok := value.(json.Number)
			if !ok {
				return errors.New("configuration requires an integer")
			}
			if _, err := number.Int64(); err != nil {
				return errors.New("configuration requires an integer")
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return errors.New("configuration requires a boolean")
			}
		case "stringList":
			entries, ok := value.([]any)
			if !ok {
				return errors.New("configuration requires a string list")
			}
			seen := map[string]bool{}
			for _, entry := range entries {
				text, ok := entry.(string)
				if !ok || (len(field.Enum) > 0 && !slices.Contains(field.Enum, text)) || (field.UniqueItems && seen[text]) {
					return errors.New("configuration list contains an invalid or duplicate item")
				}
				seen[text] = true
			}
		case "stringMap":
			entries, ok := value.(map[string]any)
			if !ok {
				return errors.New("configuration requires a string map")
			}
			for _, entry := range entries {
				if _, ok := entry.(string); !ok {
					return errors.New("configuration requires string map values")
				}
			}
		default:
			return errors.New("configuration field type is unsupported")
		}
	}
	return nil
}
