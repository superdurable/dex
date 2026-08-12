// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package command

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type blobReference struct {
	id   string
	kind string
}

func naturalMessage(
	ctx context.Context,
	client dexpb.FlowServiceClient,
	message proto.Message,
	noHydrate bool,
) (map[string]any, []string, error) {
	raw, err := messageMap(message)
	if err != nil {
		return nil, nil, err
	}
	references := make(map[string]blobReference)
	collectBlobReferences(raw, references)
	if noHydrate || len(references) == 0 {
		return naturalValue(raw, nil, false).(map[string]any), nil, nil
	}
	replacements, warnings := hydrateBlobReferences(ctx, client, references)
	return naturalValue(raw, replacements, true).(map[string]any), warnings, nil
}

func messageMap(message proto.Message) (map[string]any, error) {
	data, err := (protojson.MarshalOptions{}).Marshal(message)
	if err != nil {
		return nil, err
	}
	result := make(map[string]any)
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func collectBlobReferences(value any, references map[string]blobReference) {
	switch current := value.(type) {
	case map[string]any:
		if reference, ok := rawBlobReference(current); ok {
			references[blobReferenceKey(reference)] = reference
			return
		}
		for _, entry := range current {
			collectBlobReferences(entry, references)
		}
	case []any:
		for _, entry := range current {
			collectBlobReferences(entry, references)
		}
	}
}

func hydrateBlobReferences(
	ctx context.Context,
	client dexpb.FlowServiceClient,
	references map[string]blobReference,
) (map[string]any, []string) {
	values := make([]*dexpb.Value, 0, len(references))
	for _, reference := range references {
		value := &dexpb.Value{}
		if reference.kind == "string" {
			value.Kind = &dexpb.Value_InternalBlobIdForStringValue{
				InternalBlobIdForStringValue: reference.id,
			}
		} else {
			value.Kind = &dexpb.Value_InternalBlobIdForObjValue{
				InternalBlobIdForObjValue: reference.id,
			}
		}
		values = append(values, value)
	}
	response, err := client.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{Values: values})
	if err != nil {
		return nil, []string{fmt.Sprintf("stored values unavailable: %v", err)}
	}
	replacements := make(map[string]any, len(references))
	missing := 0
	for key, reference := range references {
		value, found := response.GetValues()[reference.id]
		if !found {
			missing++
			continue
		}
		raw, mapErr := messageMap(value)
		if mapErr != nil {
			missing++
			continue
		}
		replacements[key] = naturalValue(raw, nil, false)
	}
	if missing > 0 {
		return replacements, []string{fmt.Sprintf("%d stored value(s) unavailable", missing)}
	}
	return replacements, nil
}

func naturalValue(value any, replacements map[string]any, unavailableWhenMissing bool) any {
	switch current := value.(type) {
	case map[string]any:
		if reference, ok := rawBlobReference(current); ok {
			if replacement, found := replacements[blobReferenceKey(reference)]; found {
				return replacement
			}
			if unavailableWhenMissing {
				return map[string]any{
					"__dexStoredValueUnavailable": true,
					"__dexBlobReference":          mappedBlobReference(reference),
				}
			}
			return map[string]any{"__dexBlobReference": mappedBlobReference(reference)}
		}
		if mapped, ok := concreteDexValue(current); ok {
			return mapped
		}
		mapped := make(map[string]any, len(current))
		for key, entry := range current {
			mapped[key] = naturalValue(entry, replacements, unavailableWhenMissing)
		}
		return mapped
	case []any:
		mapped := make([]any, len(current))
		for index, entry := range current {
			mapped[index] = naturalValue(entry, replacements, unavailableWhenMissing)
		}
		return mapped
	default:
		return value
	}
}

func rawBlobReference(value map[string]any) (blobReference, bool) {
	if len(value) != 1 {
		return blobReference{}, false
	}
	if id, ok := value["internalBlobIdForStringValue"].(string); ok && id != "" {
		return blobReference{id: id, kind: "string"}, true
	}
	if id, ok := value["internalBlobIdForObjValue"].(string); ok && id != "" {
		return blobReference{id: id, kind: "object"}, true
	}
	return blobReference{}, false
}

func concreteDexValue(value map[string]any) (any, bool) {
	if len(value) != 1 {
		return nil, false
	}
	if current, ok := value["stringValue"]; ok {
		return current, true
	}
	if current, ok := value["intValue"]; ok {
		stringValue, stringOK := current.(string)
		if !stringOK {
			return current, true
		}
		parsed, err := strconv.ParseInt(stringValue, 10, 64)
		if err != nil {
			return stringValue, true
		}
		return parsed, true
	}
	if current, ok := value["doubleValue"]; ok {
		return current, true
	}
	if current, ok := value["boolValue"]; ok {
		return current, true
	}
	if _, ok := value["nullValue"]; ok {
		return nil, true
	}
	if current, ok := value["objValue"].(map[string]any); ok {
		return decodedObject(current), true
	}
	return nil, false
}

func decodedObject(value map[string]any) any {
	encoding := ""
	if current, ok := value["encoding"].(string); ok {
		encoding = current
	}
	payload := ""
	if current, ok := value["payload"].(string); ok {
		payload = current
	}
	if encoding == "json" {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err == nil {
			var mapped any
			if err := json.Unmarshal(decoded, &mapped); err == nil {
				return mapped
			}
		}
	}
	return map[string]any{"encoding": encoding, "payload": payload}
}

func mappedBlobReference(reference blobReference) map[string]any {
	return map[string]any{"id": reference.id, "kind": reference.kind}
}

func blobReferenceKey(reference blobReference) string {
	return reference.kind + ":" + reference.id
}
