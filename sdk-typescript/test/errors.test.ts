// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import test from "node:test";

import { BinaryReader, BinaryWriter } from "@bufbuild/protobuf/wire";
import { Metadata, status, type ServiceError } from "@grpc/grpc-js";

import {
  DexServiceError,
  FlowAlreadyStartedError,
  FlowNotActiveError,
  FlowNotFoundError,
  LongPollTimeoutError,
  RpcLockConflictError,
  WorkerInvocationError,
} from "../src/errors.js";
import { ServiceErrorResponse, ErrorSubStatus, WorkerErrorResponse } from "../src/gen/dex.js";
import { translateServiceError, workerServiceError } from "../src/grpc-status.js";

test("missing Flow uses the endpoint lifecycle requirement", () => {
  const error = serviceError(status.NOT_FOUND, ErrorSubStatus.ERROR_SUB_STATUS_FLOW_NOT_EXISTS);
  assert.ok(
    translateServiceError(error, "describeFlow", "flow-id", "existing") instanceof
      FlowNotFoundError,
  );
  assert.ok(
    translateServiceError(error, "publish", "flow-id", "active") instanceof FlowNotActiveError,
  );
});

test("worker failures preserve worker details and isolate lock conflicts", () => {
  const invocation = translateServiceError(
    serviceError(status.FAILED_PRECONDITION, ErrorSubStatus.ERROR_SUB_STATUS_WORKER_API_ERROR, {
      originalWorkerErrorStatus: status.INVALID_ARGUMENT,
      originalWorkerErrorType: "ApplicationError",
      originalWorkerErrorDetail: "invalid order",
    }),
    "invokeRPC",
    "flow-id",
    "active",
  );
  assert.ok(invocation instanceof WorkerInvocationError);
  assert.equal(invocation.workerCode, status.INVALID_ARGUMENT);
  assert.equal(invocation.workerErrorType, "ApplicationError");
  assert.equal(invocation.workerErrorDetail, "invalid order");

  const unknownWorkerCode = translateServiceError(
    serviceError(status.FAILED_PRECONDITION, ErrorSubStatus.ERROR_SUB_STATUS_WORKER_API_ERROR, {
      originalWorkerErrorStatus: 999,
    }),
    "invokeRPC",
    "flow-id",
    "active",
  );
  assert.ok(unknownWorkerCode instanceof WorkerInvocationError);
  assert.equal(unknownWorkerCode.workerCode, status.UNKNOWN);

  const conflict = translateServiceError(
    serviceError(status.ABORTED, ErrorSubStatus.ERROR_SUB_STATUS_WORKER_API_ERROR),
    "invokeRPC",
    "flow-id",
    "active",
  );
  assert.ok(conflict instanceof RpcLockConflictError);
});

test("other known sub-statuses have explicit errors", () => {
  assert.ok(
    translateServiceError(
      serviceError(status.ALREADY_EXISTS, ErrorSubStatus.ERROR_SUB_STATUS_FLOW_ALREADY_STARTED),
      "startFlow",
      "flow-id",
      "none",
    ) instanceof FlowAlreadyStartedError,
  );
  assert.ok(
    translateServiceError(
      serviceError(status.DEADLINE_EXCEEDED, ErrorSubStatus.ERROR_SUB_STATUS_LONG_POLL_TIME_OUT),
      "waitForFlow",
      "flow-id",
      "existing",
    ) instanceof LongPollTimeoutError,
  );
});

test("missing and malformed details fall back to DexServiceError", () => {
  const missing = grpcError(status.INTERNAL, new Metadata());
  const missingResult = translateServiceError(missing, "searchFlows", undefined, "none");
  assert.equal(missingResult.constructor, DexServiceError);
  assert.equal(missingResult.cause, missing);

  const metadata = new Metadata();
  metadata.set("grpc-status-details-bin", Buffer.from([255]));
  const malformed = grpcError(status.INTERNAL, metadata);
  const malformedResult = translateServiceError(malformed, "searchFlows", undefined, "none");
  assert.equal(malformedResult.constructor, DexServiceError);
  assert.match(malformedResult.detail, /malformed error details/);
});

test("worker errors bound every text field at UTF-8 boundaries", () => {
  const failure = new Error("世界".repeat(8 * 1024));
  failure.name = "世界".repeat(512);
  failure.stack = "世界".repeat(8 * 1024);

  const error = workerServiceError(failure);
  const worker = workerErrorDetails(error);

  assert.ok(Buffer.byteLength(worker.detail) <= 1024);
  assert.ok(Buffer.byteLength(worker.errorType) <= 256);
  assert.ok(Buffer.byteLength(worker.stackTrace) <= 4 * 1024);
  assert.match(worker.detail, /error detail truncated by Dex TypeScript SDK/);
  assert.match(worker.errorType, /error type truncated by Dex TypeScript SDK/);
  assert.match(worker.stackTrace, /stack trace truncated by Dex TypeScript SDK/);
  assert.equal(error.details, worker.detail);
  assert.ok(workerErrorStatusSize(error) < 7 * 1024);
  assert.doesNotMatch(worker.detail, /\ufffd/);
  assert.doesNotMatch(worker.errorType, /\ufffd/);
  assert.doesNotMatch(worker.stackTrace, /\ufffd/);
});

function serviceError(
  code: status,
  subStatus: ErrorSubStatus,
  worker: Partial<ServiceErrorResponse> = {},
): ServiceError {
  const response = ServiceErrorResponse.encode({
    detail: "service detail",
    subStatus,
    originalWorkerErrorDetail: "",
    originalWorkerErrorType: "",
    originalWorkerErrorStatus: 0,
    originalWorkerErrorStackTrace: "",
    ...worker,
  }).finish();
  const writer = new BinaryWriter();
  writer.uint32(26).fork();
  writer.uint32(10).string("type.googleapis.com/dex.ServiceErrorResponse");
  writer.uint32(18).bytes(response);
  writer.join();
  const metadata = new Metadata();
  metadata.set("grpc-status-details-bin", Buffer.from(writer.finish()));
  return grpcError(code, metadata);
}

function grpcError(code: status, metadata: Metadata): ServiceError {
  return Object.assign(new Error("gRPC detail"), {
    code,
    details: "gRPC detail",
    metadata,
  });
}

function workerErrorDetails(error: ServiceError): WorkerErrorResponse {
  const encoded = error.metadata.get("grpc-status-details-bin")[0];
  assert.ok(encoded instanceof Buffer);
  const details = decodeStatusDetails(encoded);
  const worker = details.find((candidate) => candidate.typeUrl.endsWith("/dex.WorkerErrorResponse"));
  assert.ok(worker);
  return WorkerErrorResponse.decode(worker.value);
}

function workerErrorStatusSize(error: ServiceError): number {
  const encoded = error.metadata.get("grpc-status-details-bin")[0];
  assert.ok(encoded instanceof Buffer);
  return encoded.length;
}

function decodeStatusDetails(
  encoded: Uint8Array<ArrayBufferLike>,
): { typeUrl: string; value: Uint8Array<ArrayBufferLike> }[] {
  const reader = new BinaryReader(encoded);
  const details: { typeUrl: string; value: Uint8Array<ArrayBufferLike> }[] = [];
  while (reader.pos < reader.len) {
    const tag = reader.uint32();
    if (tag >>> 3 !== 3) {
      reader.skip(tag & 7);
      continue;
    }
    const end = reader.pos + reader.uint32();
    let typeUrl = "";
    let value: Uint8Array<ArrayBufferLike> = new Uint8Array();
    while (reader.pos < end) {
      const detailTag = reader.uint32();
      if (detailTag >>> 3 === 1) {
        typeUrl = reader.string();
      } else if (detailTag >>> 3 === 2) {
        value = reader.bytes();
      } else {
        reader.skip(detailTag & 7);
      }
    }
    details.push({ typeUrl, value });
  }
  return details;
}
