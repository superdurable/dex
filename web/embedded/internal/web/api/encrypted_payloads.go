// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	// payloadEnvelopeEncoding marks an object Value that holds a serialized PayloadEnvelope.
	payloadEnvelopeEncoding = "dex/1"
	// reservedEncodingPrefix starts every encoding reserved for Dex payload formats.
	reservedEncodingPrefix = "dex/"
	// maxPayloadEnvelopeBytes is the largest envelope readers accept, per the dex/1 format.
	maxPayloadEnvelopeBytes = 16_842_752
	// encryptedEnvelopeField marks an encrypted stored Value in Dex Web JSON.
	encryptedEnvelopeField = "__dexEnvelope"
)

// encryptedValueMessage tells the user why Dex Web cannot use a value; the browser alone holds the codec.
const encryptedValueMessage = "is encrypted; Dex Web cannot read encrypted values. " +
	"Declare the Attribute Plaintext to use it here"

// encodedTextPrefix starts an encrypted error text or reason.
const encodedTextPrefix = "dex-encoded:"

// encryptedErrorMessage replaces an encrypted gRPC status message for readers without a codec endpoint.
const encryptedErrorMessage = "The request failed with an encrypted error message. Set a codec endpoint to read it."

// isEncodedStatusMessage reports whether message is an encrypted gRPC status message that readers decode.
// Only a status carrying a ServiceErrorResponse detail is such a read point; any other status stays unchanged.
func isEncodedStatusMessage(statusError *status.Status, message string) bool {
	if !strings.HasPrefix(message, encodedTextPrefix) || message != statusError.Message() {
		return false
	}
	for _, detail := range statusError.Details() {
		if _, ok := detail.(*dexpb.ServiceErrorResponse); ok {
			return true
		}
	}
	return false
}

// encodedStoredValue parses a Value that the browser already encoded with the codec endpoint.
//
// It accepts only a dex/1 object Value and passes it to the Server unchanged; Dex Web never decodes it.
func encodedStoredValue(raw json.RawMessage) (*dexpb.Value, error) {
	value := &dexpb.Value{}
	if err := protojson.Unmarshal(raw, value); err != nil {
		return nil, fmt.Errorf("encoded value must be Value JSON")
	}
	object := value.GetObjValue()
	if object == nil || object.GetEncoding() != payloadEnvelopeEncoding {
		return nil, fmt.Errorf("encoded value must be an object Value with encoding %s", payloadEnvelopeEncoding)
	}
	if len(object.GetPayload()) == 0 || len(object.GetPayload()) > maxPayloadEnvelopeBytes {
		return nil, fmt.Errorf("encoded value payload must hold 1 to %d bytes", maxPayloadEnvelopeBytes)
	}
	return value, nil
}

// hasEncodedValue reports whether a request carries an encodedValue field.
func hasEncodedValue(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

// debugOrEncodedValue returns the encoded Value when the request has one, or encodes the typed debug value.
func debugOrEncodedValue(value interface{}, valueType string, encoded json.RawMessage) (*dexpb.Value, error) {
	if !hasEncodedValue(encoded) {
		return encodeDebugValue(value, valueType)
	}
	if value != nil || valueType != "" {
		return nil, fmt.Errorf("send either encodedValue or valueType and value, not both")
	}
	return encodedStoredValue(encoded)
}

// isEncryptedValue reports whether a stored Value uses a reserved Dex encoding, such as a dex/1 envelope.
func isEncryptedValue(value *dexpb.Value) bool {
	return strings.HasPrefix(value.GetObjValue().GetEncoding(), reservedEncodingPrefix)
}

// escapedObjectField wraps an application JSON object that has a reserved "__dex" key, so it never reads as
// a marker Dex Web generated, such as an encrypted envelope or a blob reference.
const escapedObjectField = "__dexEscapedObject"

// reservedMarkerKeyPrefix starts every marker key that Dex Web and its browser code generate.
const reservedMarkerKeyPrefix = "__dex"

// escapeApplicationJSON wraps, at every depth, each object that has a key starting with "__dex" as
// {"__dexEscapedObject": object}. unescapeApplicationJSON reverses it exactly.
func escapeApplicationJSON(value interface{}) interface{} {
	switch current := value.(type) {
	case map[string]interface{}:
		hasReservedKey := false
		for key, entry := range current {
			current[key] = escapeApplicationJSON(entry)
			if strings.HasPrefix(key, reservedMarkerKeyPrefix) {
				hasReservedKey = true
			}
		}
		if hasReservedKey {
			return map[string]interface{}{escapedObjectField: current}
		}
		return current
	case []interface{}:
		for index, entry := range current {
			current[index] = escapeApplicationJSON(entry)
		}
		return current
	default:
		return value
	}
}

// unwrapEscapedObject removes one escape wrapper from value, keeping nested wrappers.
func unwrapEscapedObject(value interface{}) interface{} {
	if current, ok := value.(map[string]interface{}); ok && len(current) == 1 {
		if escaped, ok := current[escapedObjectField].(map[string]interface{}); ok {
			return escaped
		}
	}
	return value
}

// unescapeApplicationJSON returns application JSON without the wrappers escapeApplicationJSON added.
func unescapeApplicationJSON(value interface{}) interface{} {
	switch current := value.(type) {
	case map[string]interface{}:
		if escaped, ok := current[escapedObjectField].(map[string]interface{}); ok && len(current) == 1 {
			current = escaped
		}
		unescaped := make(map[string]interface{}, len(current))
		for key, entry := range current {
			unescaped[key] = unescapeApplicationJSON(entry)
		}
		return unescaped
	case []interface{}:
		unescaped := make([]interface{}, len(current))
		for index, entry := range current {
			unescaped[index] = unescapeApplicationJSON(entry)
		}
		return unescaped
	default:
		return value
	}
}

// mappedEncryptedEnvelope marks a stored Value with a reserved Dex encoding in Dex Web JSON.
//
// Application JSON with "__dex" keys is escaped by escapeApplicationJSON, so the browser decodes only Values
// Dex Web marked, never application JSON that merely looks like an envelope.
func mappedEncryptedEnvelope(encoding string, payloadBase64 string) map[string]interface{} {
	return map[string]interface{}{
		encryptedEnvelopeField: map[string]interface{}{"encoding": encoding, "payload": payloadBase64},
	}
}

// protoJSONEncryptedEnvelope returns the marker for a protojson Value {"objValue": {...}} with a reserved
// encoding, or nil for any other map.
func protoJSONEncryptedEnvelope(value map[string]interface{}) map[string]interface{} {
	if len(value) != 1 {
		return nil
	}
	object, ok := value["objValue"].(map[string]interface{})
	if !ok {
		return nil
	}
	encoding, _ := object["encoding"].(string)
	if !strings.HasPrefix(encoding, reservedEncodingPrefix) {
		return nil
	}
	payload, _ := object["payload"].(string)
	return mappedEncryptedEnvelope(encoding, payload)
}

// isEncryptedMappedValue reports whether a value mapped by dexValue is a Value with a reserved Dex encoding.
func isEncryptedMappedValue(value interface{}) bool {
	mapped, ok := value.(map[string]interface{})
	if !ok || len(mapped) != 1 {
		return false
	}
	_, ok = mapped[encryptedEnvelopeField].(map[string]interface{})
	return ok
}

// actionConditionValueError explains why an Action condition cannot read an Attribute value, or returns nil.
func actionConditionValueError(attributeKey string, value interface{}) error {
	if isEncryptedMappedValue(value) {
		return fmt.Errorf("Action condition Attribute %q %s", attributeKey, encryptedValueMessage)
	}
	if isBlobReferenceMappedValue(value) {
		return fmt.Errorf("Action condition Attribute %q could not be loaded", attributeKey)
	}
	return nil
}

// isBlobReferenceMappedValue reports whether a mapped value is a blob reference Dex Web has not loaded.
func isBlobReferenceMappedValue(value interface{}) bool {
	mapped, ok := value.(map[string]interface{})
	if !ok || len(mapped) != 1 {
		return false
	}
	_, ok = mapped[blobReferenceField]
	return ok
}
