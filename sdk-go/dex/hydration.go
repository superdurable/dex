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
	"log/slog"
	"unicode/utf8"

	"github.com/superdurable/dex/sdk-go/dex/blobcache"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

type valueHydrator interface {
	HydrateValuesInPlace(context.Context, []**dexpb.Value) error
}

type valueHydratorImpl struct {
	client dexpb.FlowServiceClient
	cache  *blobcache.Cache
}

type blobIDDef struct {
	value            string
	isObjectOrString bool
}

type blobHydrationRequest struct {
	value  *dexpb.Value
	blobID blobIDDef
}

type blobValueTarget struct {
	value  **dexpb.Value
	blobID blobIDDef
}

func newValueHydrator(
	client dexpb.FlowServiceClient,
	cache *blobcache.Cache,
) valueHydrator {
	if client == nil {
		panic("dex: value hydrator requires FlowService client")
	}
	return &valueHydratorImpl{client: client, cache: cache}
}

func (hydrator *valueHydratorImpl) HydrateValuesInPlace(
	ctx context.Context,
	valuePointers []**dexpb.Value,
) error {
	requests := make([]blobHydrationRequest, 0, len(valuePointers))
	targets := make([]blobValueTarget, 0, len(valuePointers))
	seenBlobIDs := make(map[blobIDDef]struct{}, len(valuePointers))
	for index, valuePointer := range valuePointers {
		if valuePointer == nil {
			return newWorkerFailure(
				codes.InvalidArgument,
				fmt.Errorf("dex: value pointer at index %d is nil", index),
			)
		}
		blobID, found, err := getBlobID(*valuePointer)
		if err != nil {
			return newWorkerFailure(codes.InvalidArgument, err)
		}
		if !found {
			if err := validateConcreteValue(*valuePointer); err != nil {
				return newWorkerFailure(codes.InvalidArgument, err)
			}
			continue
		}
		targets = append(targets, blobValueTarget{
			value:  valuePointer,
			blobID: blobID,
		})
		if _, found := seenBlobIDs[blobID]; found {
			continue
		}
		seenBlobIDs[blobID] = struct{}{}
		requests = append(requests, blobHydrationRequest{
			value:  *valuePointer,
			blobID: blobID,
		})
	}
	if len(requests) == 0 {
		return nil
	}

	hydratedValues, err := hydrator.hydrateBlobValues(ctx, requests)
	if err != nil {
		return err
	}
	for _, target := range targets {
		*target.value = hydratedValues[target.blobID]
	}
	return nil
}

func (hydrator *valueHydratorImpl) hydrateBlobValues(
	ctx context.Context,
	requests []blobHydrationRequest,
) (map[blobIDDef]*dexpb.Value, error) {
	values := make(map[blobIDDef]*dexpb.Value, len(requests))
	misses := make([]blobHydrationRequest, 0, len(requests))
	for _, request := range requests {
		cached, found := hydrator.loadCached(request.value, request.blobID)
		if found {
			values[request.blobID] = cached
			continue
		}
		misses = append(misses, request)
	}
	if len(misses) == 0 {
		return values, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	response, err := hydrator.client.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{
		Values: blobHydrationRequestValues(misses),
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, newWorkerFailure(
			codes.Internal,
			fmt.Errorf("dex: LoadBlobs: %w", err),
		)
	}
	if response == nil {
		return nil, newWorkerFailure(
			codes.Internal,
			fmt.Errorf("dex: LoadBlobs returned a nil response"),
		)
	}
	for _, miss := range misses {
		concrete, found := response.Values[miss.blobID.value]
		if !found {
			return nil, newWorkerFailure(
				codes.Internal,
				fmt.Errorf("dex: LoadBlobs omitted blob %q", miss.blobID.value),
			)
		}
		if err := validateHydratedValue(miss.blobID, concrete); err != nil {
			return nil, newWorkerFailure(codes.Internal, err)
		}
		values[miss.blobID] = concrete
		hydrator.storeCached(miss.value, miss.blobID, concrete)
	}
	return values, nil
}

func (hydrator *valueHydratorImpl) loadCached(
	request *dexpb.Value,
	blobID blobIDDef,
) (*dexpb.Value, bool) {
	if hydrator.cache == nil {
		return nil, false
	}
	payload, found, err := hydrator.cache.Get(blobID.value)
	if err != nil {
		slog.Default().Warn("read Worker blob cache", "blob_id", blobID.value, "error", err)
		return nil, false
	}
	if !found {
		return nil, false
	}
	value, err := unmarshalBlobCachePayload(request, payload)
	if err == nil {
		return value, true
	}
	slog.Default().Warn("decode Worker blob cache", "blob_id", blobID.value, "error", err)
	if deleteErr := hydrator.cache.Delete(blobID.value); deleteErr != nil {
		slog.Default().Warn(
			"delete Worker blob cache entry",
			"blob_id", blobID.value,
			"error", deleteErr,
		)
	}
	return nil, false
}

func (hydrator *valueHydratorImpl) storeCached(
	request *dexpb.Value,
	blobID blobIDDef,
	concrete *dexpb.Value,
) {
	if hydrator.cache == nil {
		return
	}
	payload, err := marshalBlobCachePayload(request, concrete)
	if err != nil {
		slog.Default().Warn("encode Worker blob cache", "blob_id", blobID.value, "error", err)
		return
	}
	cached, err := hydrator.cache.Put(blobID.value, payload)
	if err != nil {
		slog.Default().Warn("write Worker blob cache", "blob_id", blobID.value, "error", err)
		return
	}
	if !cached {
		slog.Default().Debug("Worker blob cache rejected entry", "blob_id", blobID.value)
	}
}

func blobHydrationRequestValues(
	requests []blobHydrationRequest,
) []*dexpb.Value {
	values := make([]*dexpb.Value, 0, len(requests))
	for _, request := range requests {
		values = append(values, request.value)
	}
	return values
}

func marshalBlobCachePayload(
	blobIDValue *dexpb.Value,
	concreteValue *dexpb.Value,
) ([]byte, error) {
	blobID, found, err := getBlobID(blobIDValue)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("dex: cache payload requires a blob ID")
	}
	if err := validateHydratedValue(blobID, concreteValue); err != nil {
		return nil, err
	}
	if blobID.isObjectOrString {
		return proto.MarshalOptions{Deterministic: true}.Marshal(
			concreteValue.GetObjValue(),
		)
	}
	return []byte(concreteValue.GetStringValue()), nil
}

func unmarshalBlobCachePayload(
	blobIDValue *dexpb.Value,
	payload []byte,
) (*dexpb.Value, error) {
	blobID, found, err := getBlobID(blobIDValue)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("dex: cache payload requires a blob ID")
	}
	if !blobID.isObjectOrString {
		if !utf8.Valid(payload) {
			return nil, fmt.Errorf("dex: cached string blob is not valid UTF-8")
		}
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

func getBlobID(value *dexpb.Value) (blobIDDef, bool, error) {
	if value == nil || value.Kind == nil {
		return blobIDDef{}, false, fmt.Errorf("dex: value has no kind")
	}
	switch kind := value.Kind.(type) {
	case *dexpb.Value_InternalBlobIdForStringValue:
		if kind.InternalBlobIdForStringValue == "" {
			return blobIDDef{}, false, fmt.Errorf("dex: string blob ID is empty")
		}
		return blobIDDef{value: kind.InternalBlobIdForStringValue}, true, nil
	case *dexpb.Value_InternalBlobIdForObjValue:
		if kind.InternalBlobIdForObjValue == "" {
			return blobIDDef{}, false, fmt.Errorf("dex: object blob ID is empty")
		}
		return blobIDDef{
			value:            kind.InternalBlobIdForObjValue,
			isObjectOrString: true,
		}, true, nil
	default:
		return blobIDDef{}, false, nil
	}
}

func validateHydratedValue(
	blobID blobIDDef,
	value *dexpb.Value,
) error {
	if blobID.isObjectOrString {
		if _, ok := value.GetKind().(*dexpb.Value_ObjValue); !ok {
			return fmt.Errorf("dex: object blob %q hydrated to %T", blobID.value, value.GetKind())
		}
	} else {
		if _, ok := value.GetKind().(*dexpb.Value_StringValue); !ok {
			return fmt.Errorf("dex: string blob %q hydrated to %T", blobID.value, value.GetKind())
		}
	}
	return validateConcreteValue(value)
}
