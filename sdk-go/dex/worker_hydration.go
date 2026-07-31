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

	"github.com/superdurable/dex/sdk-go/dex/blobcache"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
)

type workerValueHydrator struct {
	client dexpb.FlowServiceClient
	cache  *blobcache.Cache
}

type hydrationMiss struct {
	request   *dexpb.Value
	reference blobReference
	position  int
}

func newWorkerValueHydrator(
	client dexpb.FlowServiceClient,
	cache *blobcache.Cache,
) *workerValueHydrator {
	if client == nil {
		panic("dex: worker value hydrator requires FlowService client")
	}
	return &workerValueHydrator{client: client, cache: cache}
}

func (hydrator *workerValueHydrator) Hydrate(
	ctx context.Context,
	requests []*dexpb.Value,
) ([]*dexpb.Value, error) {
	results := make([]*dexpb.Value, len(requests))
	misses := make([]hydrationMiss, 0, len(requests))
	for position, request := range requests {
		reference, blob, err := classifyBlob(request)
		if err != nil {
			return nil, err
		}
		if !blob {
			return nil, fmt.Errorf("dex: hydration request at index %d is not a blob", position)
		}
		cached, found := hydrator.loadCached(request, reference)
		if found {
			results[position] = cached
			continue
		}
		misses = append(misses, hydrationMiss{
			request:   request,
			reference: reference,
			position:  position,
		})
	}
	if len(misses) == 0 {
		return results, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	response, err := hydrator.client.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{
		Values: hydrationMissRequests(misses),
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
		concrete, found := response.Values[miss.reference.id]
		if !found {
			return nil, newWorkerFailure(
				codes.Internal,
				fmt.Errorf("dex: LoadBlobs omitted blob %q", miss.reference.id),
			)
		}
		if err := validateHydratedValue(miss.reference, concrete); err != nil {
			return nil, newWorkerFailure(codes.Internal, err)
		}
		results[miss.position] = concrete
		hydrator.storeCached(miss.request, miss.reference, concrete)
	}
	return results, nil
}

func (hydrator *workerValueHydrator) loadCached(
	request *dexpb.Value,
	reference blobReference,
) (*dexpb.Value, bool) {
	if hydrator.cache == nil {
		return nil, false
	}
	payload, found, err := hydrator.cache.Get(reference.id)
	if err != nil {
		slog.Default().Warn("read Worker blob cache", "blob_id", reference.id, "error", err)
		return nil, false
	}
	if !found {
		return nil, false
	}
	value, err := unmarshalBlobCachePayload(request, payload)
	if err == nil {
		return value, true
	}
	slog.Default().Warn("decode Worker blob cache", "blob_id", reference.id, "error", err)
	if deleteErr := hydrator.cache.Delete(reference.id); deleteErr != nil {
		slog.Default().Warn(
			"delete Worker blob cache entry",
			"blob_id", reference.id,
			"error", deleteErr,
		)
	}
	return nil, false
}

func (hydrator *workerValueHydrator) storeCached(
	request *dexpb.Value,
	reference blobReference,
	concrete *dexpb.Value,
) {
	if hydrator.cache == nil {
		return
	}
	payload, err := marshalBlobCachePayload(request, concrete)
	if err != nil {
		slog.Default().Warn("encode Worker blob cache", "blob_id", reference.id, "error", err)
		return
	}
	cached, err := hydrator.cache.Put(reference.id, payload)
	if err != nil {
		slog.Default().Warn("write Worker blob cache", "blob_id", reference.id, "error", err)
		return
	}
	if !cached {
		slog.Default().Debug("Worker blob cache rejected entry", "blob_id", reference.id)
	}
}

func hydrationMissRequests(misses []hydrationMiss) []*dexpb.Value {
	requests := make([]*dexpb.Value, 0, len(misses))
	for _, miss := range misses {
		requests = append(requests, miss.request)
	}
	return requests
}

func hydrateValueSlots(
	ctx context.Context,
	hydrator valueHydrator,
	slots []**dexpb.Value,
) error {
	values := make([]*dexpb.Value, len(slots))
	for index, slot := range slots {
		if slot == nil {
			return fmt.Errorf("dex: value slot at index %d is nil", index)
		}
		values[index] = *slot
	}
	hydrated, err := hydrateValues(ctx, hydrator, values)
	if err != nil {
		return err
	}
	for index, slot := range slots {
		*slot = hydrated[index]
	}
	return nil
}
