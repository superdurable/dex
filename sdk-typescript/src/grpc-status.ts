// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { BinaryReader, BinaryWriter } from "@bufbuild/protobuf/wire";
import { Metadata, status, type ServiceError } from "@grpc/grpc-js";

import {
  ErrorResponse,
  ErrorSubStatus as ProtoErrorSubStatus,
  WorkerErrorResponse,
} from "./gen/dex.js";
import { DexError, ErrorSubStatus, type ErrorSubStatus as ErrorSubStatusValue } from "./errors.js";

const statusDetailsKey = "grpc-status-details-bin";

interface AnyDetail {
  readonly typeUrl: string;
  readonly value: Uint8Array;
}

export function workerServiceError(failure: unknown): ServiceError {
  const cause = failure instanceof Error ? failure : new Error(String(failure));
  const detail = cause.message || cause.name;
  const worker = WorkerErrorResponse.encode({ detail, errorType: cause.name }).finish();
  const metadata = new Metadata();
  metadata.set(
    statusDetailsKey,
    Buffer.from(
      encodeStatus(status.UNKNOWN, detail, [
        { typeUrl: "type.googleapis.com/dex.WorkerErrorResponse", value: worker },
      ]),
    ),
  );
  return Object.assign(new Error(detail), {
    code: status.UNKNOWN,
    details: detail,
    metadata,
  });
}

export function translateServiceError(error: ServiceError): DexError {
  const response = errorResponse(error);
  const detail = response?.detail || error.details || error.message;
  return new DexError(
    error.code,
    response === undefined ? undefined : mapSubStatus(response.subStatus),
    detail,
    response?.originalWorkerErrorType ?? "",
    response?.originalWorkerErrorDetail ?? "",
    { cause: error },
  );
}

function errorResponse(error: ServiceError): ErrorResponse | undefined {
  const encoded = error.metadata.get(statusDetailsKey)[0];
  if (!(encoded instanceof Buffer)) {
    return undefined;
  }
  const decoded = decodeStatus(encoded);
  const detail = decoded.details.find((candidate) =>
    candidate.typeUrl.endsWith("/dex.ErrorResponse"),
  );
  return detail === undefined ? undefined : ErrorResponse.decode(detail.value);
}

function mapSubStatus(value: ProtoErrorSubStatus): ErrorSubStatusValue | undefined {
  switch (value) {
    case ProtoErrorSubStatus.ERROR_SUB_STATUS_UNCATEGORIZED:
      return ErrorSubStatus.UNCATEGORIZED;
    case ProtoErrorSubStatus.ERROR_SUB_STATUS_FLOW_ALREADY_STARTED:
      return ErrorSubStatus.FLOW_ALREADY_STARTED;
    case ProtoErrorSubStatus.ERROR_SUB_STATUS_FLOW_NOT_EXISTS:
      return ErrorSubStatus.FLOW_NOT_EXISTS;
    case ProtoErrorSubStatus.ERROR_SUB_STATUS_WORKER_API_ERROR:
      return ErrorSubStatus.WORKER_API_ERROR;
    case ProtoErrorSubStatus.ERROR_SUB_STATUS_LONG_POLL_TIME_OUT:
      return ErrorSubStatus.LONG_POLL_TIMEOUT;
    default:
      return undefined;
  }
}

function encodeStatus(code: number, message: string, details: readonly AnyDetail[]): Uint8Array {
  const writer = new BinaryWriter();
  if (code !== 0) {
    writer.uint32(8).int32(code);
  }
  if (message !== "") {
    writer.uint32(18).string(message);
  }
  for (const detail of details) {
    writer.uint32(26).fork();
    writer.uint32(10).string(detail.typeUrl);
    writer.uint32(18).bytes(detail.value);
    writer.join();
  }
  return writer.finish();
}

function decodeStatus(input: Uint8Array): { details: AnyDetail[] } {
  const reader = new BinaryReader(input);
  const details: AnyDetail[] = [];
  while (reader.pos < reader.len) {
    const tag = reader.uint32();
    if (tag >>> 3 === 3) {
      details.push(decodeAny(reader, reader.uint32()));
    } else {
      reader.skip(tag & 7);
    }
  }
  return { details };
}

function decodeAny(reader: BinaryReader, length: number): AnyDetail {
  const end = reader.pos + length;
  let typeUrl = "";
  let value: Uint8Array<ArrayBufferLike> = new Uint8Array();
  while (reader.pos < end) {
    const tag = reader.uint32();
    if (tag >>> 3 === 1) {
      typeUrl = reader.string();
    } else if (tag >>> 3 === 2) {
      value = reader.bytes();
    } else {
      reader.skip(tag & 7);
    }
  }
  return { typeUrl, value };
}
