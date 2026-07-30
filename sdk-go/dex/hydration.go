// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dex

import (
	"context"
	"fmt"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/proto"
)

type valueHydrator interface {
	Hydrate(context.Context, []*dexpb.Value) ([]*dexpb.Value, error)
}

type blobReference struct {
	id         string
	objectBlob bool
}

func hydrateValues(
	ctx context.Context,
	hydrator valueHydrator,
	values []*dexpb.Value,
) ([]*dexpb.Value, error) {
	result := make([]*dexpb.Value, len(values))
	references := make(map[blobReference]int)
	var requests []*dexpb.Value
	var positions []blobReference

	for index, value := range values {
		reference, blob, err := classifyBlob(value)
		if err != nil {
			return nil, err
		}
		if !blob {
			if err := validateConcreteValue(value); err != nil {
				return nil, err
			}
			result[index] = value
			continue
		}
		requestIndex, found := references[reference]
		if !found {
			requestIndex = len(requests)
			references[reference] = requestIndex
			requests = append(requests, value)
		}
		positions = append(positions, reference)
	}
	if len(requests) == 0 {
		return result, nil
	}
	if hydrator == nil {
		return nil, fmt.Errorf("dex: blob values require a hydrator")
	}
	hydrated, err := hydrator.Hydrate(ctx, requests)
	if err != nil {
		return nil, fmt.Errorf("dex: hydrate blob values: %w", err)
	}
	if len(hydrated) != len(requests) {
		return nil, fmt.Errorf(
			"dex: hydrator returned %d values for %d requests",
			len(hydrated),
			len(requests),
		)
	}
	for index, request := range requests {
		reference, _, classifyErr := classifyBlob(request)
		if classifyErr != nil {
			return nil, classifyErr
		}
		if err := validateHydratedValue(reference, hydrated[index]); err != nil {
			return nil, err
		}
	}

	blobPosition := 0
	for index, value := range result {
		if value != nil {
			continue
		}
		reference := positions[blobPosition]
		blobPosition++
		result[index] = hydrated[references[reference]]
	}
	return result, nil
}

func marshalBlobCachePayload(
	referenceValue *dexpb.Value,
	concreteValue *dexpb.Value,
) ([]byte, error) {
	reference, blob, err := classifyBlob(referenceValue)
	if err != nil {
		return nil, err
	}
	if !blob {
		return nil, fmt.Errorf("dex: cache payload requires a blob reference")
	}
	if err := validateHydratedValue(reference, concreteValue); err != nil {
		return nil, err
	}
	if reference.objectBlob {
		return proto.MarshalOptions{Deterministic: true}.Marshal(
			concreteValue.GetObjValue(),
		)
	}
	return []byte(concreteValue.GetStringValue()), nil
}

func unmarshalBlobCachePayload(
	referenceValue *dexpb.Value,
	payload []byte,
) (*dexpb.Value, error) {
	reference, blob, err := classifyBlob(referenceValue)
	if err != nil {
		return nil, err
	}
	if !blob {
		return nil, fmt.Errorf("dex: cache payload requires a blob reference")
	}
	if !reference.objectBlob {
		return &dexpb.Value{
			Kind: &dexpb.Value_StringValue{StringValue: string(payload)},
		}, nil
	}
	object := &dexpb.EncodedObject{}
	if err := proto.Unmarshal(payload, object); err != nil {
		return nil, fmt.Errorf("dex: decode cached object blob: %w", err)
	}
	value := &dexpb.Value{
		Kind: &dexpb.Value_ObjValue{ObjValue: object},
	}
	if err := validateConcreteValue(value); err != nil {
		return nil, err
	}
	return value, nil
}

func classifyBlob(value *dexpb.Value) (blobReference, bool, error) {
	if value == nil || value.Kind == nil {
		return blobReference{}, false, fmt.Errorf("dex: value has no kind")
	}
	switch kind := value.Kind.(type) {
	case *dexpb.Value_InternalBlobIdForStringValue:
		if kind.InternalBlobIdForStringValue == "" {
			return blobReference{}, false, fmt.Errorf("dex: string blob ID is empty")
		}
		return blobReference{id: kind.InternalBlobIdForStringValue}, true, nil
	case *dexpb.Value_InternalBlobIdForObjValue:
		if kind.InternalBlobIdForObjValue == "" {
			return blobReference{}, false, fmt.Errorf("dex: object blob ID is empty")
		}
		return blobReference{
			id:         kind.InternalBlobIdForObjValue,
			objectBlob: true,
		}, true, nil
	default:
		return blobReference{}, false, nil
	}
}

func validateHydratedValue(
	reference blobReference,
	value *dexpb.Value,
) error {
	if reference.objectBlob {
		if _, ok := value.GetKind().(*dexpb.Value_ObjValue); !ok {
			return fmt.Errorf("dex: object blob %q hydrated to %T", reference.id, value.GetKind())
		}
	} else {
		if _, ok := value.GetKind().(*dexpb.Value_StringValue); !ok {
			return fmt.Errorf("dex: string blob %q hydrated to %T", reference.id, value.GetKind())
		}
	}
	return validateConcreteValue(value)
}
