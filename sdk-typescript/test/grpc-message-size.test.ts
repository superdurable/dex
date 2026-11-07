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
const receiveLimitMessage = "Received message larger than max (9 vs 5)";
const serverSendLimitMessage =
  "Error, encoded message length too large: found 37748736 bytes, the limit is: 33554432 bytes";
/** Every message size failure that the pinned grpc-go, grpc-core, grpc-js, and tonic emit. */
const transportMessageSizeFailures: readonly (readonly [string, status, string])[] = [
  ["grpc-go receive", status.RESOURCE_EXHAUSTED, "grpc: received message larger than max (37748736 vs. 33554432)"],
  ["grpc-go receive on current machine", status.RESOURCE_EXHAUSTED, "grpc: received message larger than max length allowed on current machine (37748736 vs. 33554432)"],
  ["grpc-go decompressed receive", status.RESOURCE_EXHAUSTED, "grpc: message after decompression larger than max (37748736 vs. 33554432)"],
  ["grpc-go decompressed stream receive", status.RESOURCE_EXHAUSTED, "grpc: received message after decompression larger than max 33554432"],
  ["grpc-go server send", status.RESOURCE_EXHAUSTED, "grpc: trying to send message larger than max (37748736 vs. 33554432)"],
  ["grpc-go client send", status.RESOURCE_EXHAUSTED, "trying to send message larger than max (37748736 vs. 33554432)"],
  ["grpc-go marshal", status.RESOURCE_EXHAUSTED, "grpc: message too large (4294967296 bytes)"],
  ["grpc-core client receive", status.RESOURCE_EXHAUSTED, "CLIENT: Received message larger than max (37748736 vs. 33554432)"],
  ["grpc-core client send", status.RESOURCE_EXHAUSTED, "CLIENT: Sent message larger than max (37748736 vs. 33554432)"],
  ["grpc-core server receive", status.RESOURCE_EXHAUSTED, "SERVER: Received message larger than max (37748736 vs. 33554432)"],
  ["grpc-js receive", status.RESOURCE_EXHAUSTED, "Received message larger than max (37748736 vs 33554432)"],
  ["grpc-js decompressed receive", status.RESOURCE_EXHAUSTED, "Received message that decompresses to a size larger than 33554432"],
  ["grpc-js client send", status.RESOURCE_EXHAUSTED, "Attempted to send message with a size larger than 33554432"],
  ["grpc-js server send", status.RESOURCE_EXHAUSTED, "Sent message larger than max (37748736 vs. 33554432)"],
  ["tonic encode", status.OUT_OF_RANGE, "Error, encoded message length too large: found 37748736 bytes, the limit is: 33554432 bytes"],
  ["tonic decode", status.OUT_OF_RANGE, "Error, decoded message length too large: found 37748736 bytes, the limit is: 33554432 bytes"],
  ["tonic decompress", status.RESOURCE_EXHAUSTED, "Error decompressing: size limit, of 33554432 bytes, exceeded while decompressing message"],
];

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

test("a RESOURCE_EXHAUSTED or OUT_OF_RANGE size diagnostic without Dex details is a size error", () => {
  for (const [name, code, message] of transportMessageSizeFailures) {
    assert.equal(isGrpcMessageSizeError(grpcError(code, message)), true, name);
  }
  assert.equal(isGrpcMessageSizeError(grpcError(status.RESOURCE_EXHAUSTED, receiveLimitMessage, emptyStatusDetails())), true);
  assert.equal(isGrpcMessageSizeError(grpcError(status.OUT_OF_RANGE, serverSendLimitMessage, emptyStatusDetails())), true);
});

test("other failures are not size errors", () => {
  assert.equal(isGrpcMessageSizeError(grpcError(status.RESOURCE_EXHAUSTED, "Bandwidth exhausted")), false);
  assert.equal(isGrpcMessageSizeError(grpcError(status.RESOURCE_EXHAUSTED, "Frame size 20000 is larger than max frame size 16384")), false);
  assert.equal(isGrpcMessageSizeError(grpcError(status.RESOURCE_EXHAUSTED, receiveLimitMessage, dexStatusDetails())), false);
  assert.equal(isGrpcMessageSizeError(grpcError(status.RESOURCE_EXHAUSTED, receiveLimitMessage, Buffer.from([0x1a, 0x7f]))), false);
  assert.equal(isGrpcMessageSizeError(grpcError(status.OUT_OF_RANGE, serverSendLimitMessage, dexStatusDetails())), false);
  assert.equal(isGrpcMessageSizeError(grpcError(status.OUT_OF_RANGE, "page token out of range")), false);
  assert.equal(isGrpcMessageSizeError(grpcError(status.INTERNAL, receiveLimitMessage)), false);
  assert.equal(isGrpcMessageSizeError(grpcError(status.UNAVAILABLE, "down")), false);
  assert.equal(isGrpcMessageSizeError(new Error(receiveLimitMessage)), false);
  assert.equal(isGrpcMessageSizeError(undefined), false);
});

for (const [name, code, message] of transportMessageSizeFailures) {
  test(`an oversized batch rejected with the ${name} message is split in order until it fits`, async () => {
    const service = new LimitedBlobService(2, undefined, undefined, grpcError(code, message));
    const hydrated = await hydrator(service).hydrateAll("flow", blobValues(5));
    assert.deepEqual(hydrated.map(stringOf), [0, 1, 2, 3, 4].map((index) => `value-blob-${index}`));
    assert.deepEqual(service.requestedBlobIds, [
      ["blob-0", "blob-1", "blob-2", "blob-3", "blob-4"],
      ["blob-0", "blob-1"],
      ["blob-2", "blob-3", "blob-4"],
      ["blob-2"],
      ["blob-3", "blob-4"],
    ]);
  });
}

for (const [name, sizeFailure] of sizeFailures()) {
  test(`N blobs that each need their own call at the ${name} take exactly 2N-1 calls`, async () => {
    const service = new LimitedBlobService(1, undefined, undefined, sizeFailure);
    const hydrated = await hydrator(service).hydrateAll("flow", blobValues(4));
    assert.equal(hydrated.length, 4);
    assert.equal(service.callSizes.length, 2 * 4 - 1);
  });

  test(`hydrateAll names the blob that does not fit at the ${name} after splitting`, async () => {
    const service = new LimitedBlobService(1, "blob-2", undefined, sizeFailure);
    const failure = await rejection(hydrator(service).hydrateAll("flow-1", blobValues(4)));
    assertTooLarge(failure, "flow-1", "blob-2", sizeFailure);
  });
}

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

test("a size failure from a batch of one is not retried", async () => {
  const service = new LimitedBlobService(0);
  await rejection(hydrator(service).hydrateAll("flow-1", blobValues(1)));
  assert.deepEqual(service.callSizes, [1]);
});

for (const [name, failure] of [
  ["an unavailable service", grpcError(status.UNAVAILABLE, "down")],
  ["a Dex RESOURCE_EXHAUSTED error", grpcError(status.RESOURCE_EXHAUSTED, "quota", dexStatusDetails())],
  ["a RESOURCE_EXHAUSTED error without a size diagnostic", grpcError(status.RESOURCE_EXHAUSTED, "Bandwidth exhausted")],
  ["a Dex OUT_OF_RANGE error", grpcError(status.OUT_OF_RANGE, serverSendLimitMessage, dexStatusDetails())],
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
  public readonly requestedBlobIds: string[][] = [];

  public constructor(
    private readonly maxBlobs: number,
    private readonly oversizedBlobId?: string,
    private readonly failure?: ServiceError,
    private readonly sizeFailure: ServiceError = sizeError(),
  ) {}

  public get callSizes(): number[] {
    return this.requestedBlobIds.map((blobIds) => blobIds.length);
  }

  public loadBlobs(request: LoadBlobsRequest, callback: (error: ServiceError | null, response: LoadBlobsResponse) => void): void {
    const blobIds = request.entries.map((entry) => blobIdOf(entry.blobValue));
    this.requestedBlobIds.push(blobIds);
    if (this.failure !== undefined) {
      callback(this.failure, undefined as unknown as LoadBlobsResponse);
    } else if (blobIds.length > this.maxBlobs || blobIds.includes(this.oversizedBlobId ?? "")) {
      callback(this.sizeFailure, undefined as unknown as LoadBlobsResponse);
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
  return grpcError(status.RESOURCE_EXHAUSTED, receiveLimitMessage);
}

function sizeFailures(): [string, ServiceError][] {
  return [
    ["SDK receive limit", sizeError()],
    ["server send limit", grpcError(status.OUT_OF_RANGE, serverSendLimitMessage)],
  ];
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

function assertTooLarge(failure: unknown, flowId: string, blobId: string, sizeFailure = sizeError()): void {
  assert.ok(failure instanceof BlobMessageTooLargeError);
  assert.equal(failure.blobId, blobId);
  assert.equal(failure.flowId, flowId);
  assert.equal(failure.code, status.RESOURCE_EXHAUSTED);
  assert.equal(failure.operation, "loadBlobs");
  assert.equal(failure.detail, sizeFailure.details);
  assert.ok(failure.message.startsWith(`blob "${blobId}" of Flow ID "${flowId}" exceeds the gRPC message size limit: `));
  assert.ok(isGrpcMessageSizeError(failure.cause));
  assert.equal(failure.cause.code, sizeFailure.code);
}
