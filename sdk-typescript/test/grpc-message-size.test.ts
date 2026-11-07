// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import test from "node:test";

import { Metadata, status, type ServiceError } from "@grpc/grpc-js";

import type { BlobCache } from "../src/blob-cache.js";
import { Client, Worker, Registry, BlobMessageTooLargeError, DEFAULT_GRPC_MAX_MESSAGE_BYTES } from "../src/index.js";
import { createFlowServiceChannel } from "../src/flow-service-tls.js";
import type { FlowServiceClient, LoadBlobsRequest, LoadBlobsResponse } from "../src/gen/dex.js";
import { Value as ProtoValue } from "../src/gen/dex.js";
import { isGrpcMessageSizeError, workerErrorResponse } from "../src/grpc-status.js";
import { ValueHydrator } from "../src/value-mapper.js";

const dexDetailsTypeUrl = "type.googleapis.com/dex.ServiceErrorResponse";

test("the default receive limit matches Go and Python and keeps sends unlimited", () => {
  assert.equal(DEFAULT_GRPC_MAX_MESSAGE_BYTES, 32 * 1024 * 1024);
  for (const configured of [undefined, 0]) {
    const channel = createFlowServiceChannel("localhost:8801", undefined, configured);
    assert.deepEqual(channel.channelOptions, { "grpc.max_receive_message_length": 32 * 1024 * 1024 });
    assert.equal(channel.maxReceiveMessageBytes, 32 * 1024 * 1024);
  }
});

test("a positive limit applies to both directions, on plaintext and TLS channels", () => {
  for (const tlsOptions of [undefined, {}]) {
    const channel = createFlowServiceChannel("localhost:8801", tlsOptions, 1024);
    assert.deepEqual(channel.channelOptions, {
      "grpc.max_receive_message_length": 1024,
      "grpc.max_send_message_length": 1024,
    });
    assert.equal(channel.maxReceiveMessageBytes, 1024);
  }
});

test("the TLS server name override keeps the message size options", () => {
  const channel = createFlowServiceChannel("localhost:8801", { serverName: "dex.example.com" }, 1024);
  assert.equal(channel.channelOptions["grpc.max_receive_message_length"], 1024);
  assert.equal(channel.channelOptions["grpc.ssl_target_name_override"], "dex.example.com");
});

test("Client and Worker reject an invalid grpcMaxMessageBytes", () => {
  const registry = new Registry([]);
  const cache = {} as BlobCache;
  for (const grpcMaxMessageBytes of [-1, 1.5, Number.NaN, Number.POSITIVE_INFINITY]) {
    const expected = { name: "RangeError", message: `grpcMaxMessageBytes must be a non-negative integer, got ${grpcMaxMessageBytes}` };
    assert.throws(() => new Client(registry, cache, { grpcMaxMessageBytes }), expected);
    assert.throws(() => new Worker(registry, cache, { grpcMaxMessageBytes }), expected);
  }
});

test("only a RESOURCE_EXHAUSTED error without status details is a size error", () => {
  assert.equal(isGrpcMessageSizeError(sizeError()), true);
  assert.equal(isGrpcMessageSizeError(grpcError(status.RESOURCE_EXHAUSTED, "x", emptyStatusDetails())), true);
  assert.equal(isGrpcMessageSizeError(grpcError(status.RESOURCE_EXHAUSTED, "x", dexStatusDetails())), false);
  assert.equal(isGrpcMessageSizeError(grpcError(status.RESOURCE_EXHAUSTED, "x", Buffer.from([0x1a, 0x7f]))), false);
  assert.equal(isGrpcMessageSizeError(grpcError(status.UNAVAILABLE, "down")), false);
  assert.equal(isGrpcMessageSizeError(grpcError(status.OUT_OF_RANGE, "encoded message length too large")), false);
  assert.equal(isGrpcMessageSizeError(new Error("not gRPC")), false);
  assert.equal(isGrpcMessageSizeError(undefined), false);
});

test("an oversized batch is split until it fits and returns every value in order", async () => {
  const service = new LimitedBlobService(2);
  const hydrated = await hydrator(service).hydrateAll("flow", blobValues(5));
  assert.deepEqual(hydrated.map(stringOf), [0, 1, 2, 3, 4].map((index) => `value-blob-${index}`));
  assert.equal(service.callSizes[0], 5);
  assert.ok(Math.max(...service.callSizes.slice(1)) > 1);
  assert.ok(service.callSizes.length <= 2 * 5 - 1);
});

test("N blobs that each need their own call take exactly 2N-1 calls", async () => {
  const service = new LimitedBlobService(1);
  const hydrated = await hydrator(service).hydrateAll("flow", blobValues(4));
  assert.equal(hydrated.length, 4);
  assert.equal(service.callSizes.length, 2 * 4 - 1);
});

test("a repeated blob is requested once and fills every position", async () => {
  const service = new LimitedBlobService(1);
  const [first, second] = blobValues(2);
  const hydrated = await hydrator(service).hydrateAll("flow", [first, second, first]);
  assert.deepEqual(hydrated.map(stringOf), ["value-blob-0", "value-blob-1", "value-blob-0"]);
});

test("hydrate reports one blob over the limit by name", async () => {
  const service = new LimitedBlobService(0);
  const failure = await rejection(hydrator(service).hydrate("flow-1", blobValues(1)[0]));
  assertTooLarge(failure, "flow-1", "blob-0");
  assert.equal(service.callSizes.length, 1);
});

test("hydrateAll names the blob that does not fit after splitting", async () => {
  const service = new LimitedBlobService(1, "blob-2");
  const failure = await rejection(hydrator(service).hydrateAll("flow-1", blobValues(4)));
  assertTooLarge(failure, "flow-1", "blob-2");
});

test("a size failure from a batch of one is not retried", async () => {
  const service = new LimitedBlobService(0);
  await rejection(hydrator(service).hydrateAll("flow-1", blobValues(1)));
  assert.deepEqual(service.callSizes, [1]);
});

for (const [name, failure] of [
  ["an unavailable service", grpcError(status.UNAVAILABLE, "down")],
  ["a Dex RESOURCE_EXHAUSTED error", grpcError(status.RESOURCE_EXHAUSTED, "quota", dexStatusDetails())],
] as const) {
  test(`${name} is not split and keeps its error`, async () => {
    const service = new LimitedBlobService(1, undefined, failure);
    assert.equal(await rejection(hydrator(service).hydrateAll("flow", blobValues(3))), failure);
    assert.deepEqual(service.callSizes, [3]);
  });
}

test("a Worker failure keeps the blob, Flow ID, and gRPC message", () => {
  const failure = new BlobMessageTooLargeError("flow-1", "blob-0", "Received message larger than max (9 vs 5)");
  const response = workerErrorResponse(failure);
  assert.equal(response.errorType, "BlobMessageTooLargeError");
  assert.match(response.detail, /blob "blob-0" of Flow ID "flow-1" .*larger than max \(9 vs 5\)/);
});

class LimitedBlobService {
  public readonly callSizes: number[] = [];

  public constructor(
    private readonly maxBlobs: number,
    private readonly oversizedBlobId?: string,
    private readonly failure?: ServiceError,
  ) {}

  public loadBlobs(request: LoadBlobsRequest, callback: (error: ServiceError | null, response: LoadBlobsResponse) => void): void {
    this.callSizes.push(request.entries.length);
    const blobIds = request.entries.map((entry) => blobIdOf(entry.blobValue));
    if (this.failure !== undefined) {
      callback(this.failure, undefined as unknown as LoadBlobsResponse);
    } else if (blobIds.length > this.maxBlobs || blobIds.includes(this.oversizedBlobId ?? "")) {
      callback(sizeError(), undefined as unknown as LoadBlobsResponse);
    } else {
      const values = Object.fromEntries(blobIds.map((blobId) => [blobId, stringValue(`value-${blobId}`)]));
      callback(null, { values });
    }
  }
}

function hydrator(service: LimitedBlobService): ValueHydrator {
  return new ValueHydrator(service as unknown as FlowServiceClient, memoryCache());
}

function memoryCache(): BlobCache {
  const entries = new Map<string, Uint8Array>();
  return {
    get: (blobId: string) => entries.get(blobId),
    put: (blobId: string, payload: Uint8Array) => { entries.set(blobId, payload); return true; },
  } as unknown as BlobCache;
}

function blobValues(count: number): ProtoValue[] {
  return Array.from({ length: count }, (_unused, index) => ProtoValue.create({
    kind: { $case: "internalBlobIdForStringValue", value: `blob-${index}` },
  }));
}

function stringValue(value: string): ProtoValue {
  return ProtoValue.create({ kind: { $case: "stringValue", value } });
}

function stringOf(value: ProtoValue): string {
  assert.equal(value.kind?.$case, "stringValue");
  return value.kind.value;
}

function blobIdOf(value: ProtoValue | undefined): string {
  assert.equal(value?.kind?.$case, "internalBlobIdForStringValue");
  return value.kind.value;
}

function sizeError(): ServiceError {
  return grpcError(status.RESOURCE_EXHAUSTED, "Received message larger than max (9 vs 5)");
}

function grpcError(code: status, details: string, statusDetails?: Buffer): ServiceError {
  const metadata = new Metadata();
  if (statusDetails !== undefined) metadata.set("grpc-status-details-bin", statusDetails);
  return Object.assign(new Error(`${code} ${details}`), { code, details, metadata });
}

function emptyStatusDetails(): Buffer {
  return Buffer.from([0x08, 0x08]);
}

/** Encodes a google.rpc.Status with one Any detail for dex.ServiceErrorResponse. */
function dexStatusDetails(): Buffer {
  const typeUrl = Buffer.from(dexDetailsTypeUrl);
  const anyDetail = Buffer.concat([Buffer.from([0x0a, typeUrl.length]), typeUrl]);
  return Buffer.concat([Buffer.from([0x1a, anyDetail.length]), anyDetail]);
}

async function rejection(operation: Promise<unknown>): Promise<unknown> {
  try {
    await operation;
  } catch (failure) {
    return failure;
  }
  assert.fail("expected a rejection");
}

function assertTooLarge(failure: unknown, flowId: string, blobId: string): void {
  assert.ok(failure instanceof BlobMessageTooLargeError);
  assert.equal(failure.blobId, blobId);
  assert.equal(failure.flowId, flowId);
  assert.equal(failure.code, status.RESOURCE_EXHAUSTED);
  assert.equal(failure.operation, "loadBlobs");
  assert.equal(failure.detail, "Received message larger than max (9 vs 5)");
  assert.match(failure.message, new RegExp(`blob "${blobId}" of Flow ID "${flowId}" .*larger than max`));
  assert.ok(isGrpcMessageSizeError(failure.cause));
}
