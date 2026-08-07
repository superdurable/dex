// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { BlobCache } from "./blob-cache.js";
import type { Codec, Value as CodecValue } from "./codec.js";
import { NullValue } from "./gen/google/protobuf/struct.js";
import {
  Value as ProtoValue,
  type FlowServiceClient,
  type LoadBlobsResponse,
} from "./gen/dex.js";

const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder();

export function encodeValue<T>(codec: Codec<T>, value: T): ProtoValue {
  if (value === undefined || value === null) {
    return objectValue("json", textEncoder.encode("null"));
  }
  const encoded = codec.encode(value);
  switch (encoded.kind) {
    case "string":
      return ProtoValue.create({ kind: { $case: "stringValue", value: encoded.data } });
    case "bool":
      return ProtoValue.create({ kind: { $case: "boolValue", value: encoded.data } });
    case "int64":
      return ProtoValue.create({ kind: { $case: "intValue", value: encoded.data } });
    case "double":
      return ProtoValue.create({ kind: { $case: "doubleValue", value: encoded.data } });
    case "bytes":
      return objectValue("rawbytes", encoded.data);
    case "json":
      return objectValue("json", textEncoder.encode(encoded.data));
  }
}

export function encodeUnknown(value: unknown): ProtoValue {
  if (value === undefined || value === null) {
    return objectValue("json", textEncoder.encode("null"));
  }
  if (typeof value === "string") {
    return ProtoValue.create({ kind: { $case: "stringValue", value } });
  }
  if (typeof value === "boolean") {
    return ProtoValue.create({ kind: { $case: "boolValue", value } });
  }
  if (typeof value === "bigint") {
    return ProtoValue.create({ kind: { $case: "intValue", value } });
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      throw new RangeError("non-finite numbers are unsupported");
    }
    return ProtoValue.create({ kind: { $case: "doubleValue", value } });
  }
  if (value instanceof Uint8Array) {
    return objectValue("rawbytes", value);
  }
  const json = JSON.stringify(value);
  if (json === undefined) {
    throw new TypeError("value cannot be encoded as JSON");
  }
  return objectValue("json", textEncoder.encode(json));
}

export function decodeValue<T>(codec: Codec<T>, value: ProtoValue): T {
  if (
    value.kind?.$case === "objValue" &&
    value.kind.value.encoding === "json" &&
    textDecoder.decode(value.kind.value.payload) === "null"
  ) {
    return undefined as T;
  }
  return codec.decode(toCodecValue(value));
}

export function decodeUnknown(value: ProtoValue): unknown {
  const kind = value.kind;
  if (kind === undefined) {
    throw new TypeError("Value has no concrete kind");
  }
  switch (kind.$case) {
    case "stringValue":
      return kind.value;
    case "boolValue":
      return kind.value;
    case "intValue":
      return kind.value;
    case "doubleValue":
      return kind.value;
    case "objValue": {
      const object = kind.value;
      if (object.encoding === "rawbytes") {
        return object.payload;
      }
      if (object.encoding === "json") {
        return JSON.parse(textDecoder.decode(object.payload));
      }
      throw new TypeError(`unsupported object encoding ${object.encoding}`);
    }
    case "internalBlobIdForStringValue":
    case "internalBlobIdForObjValue":
      throw new TypeError("blob-backed Value was not hydrated");
    case "nullValue":
      return undefined;
  }
}

export function deletionValue(): ProtoValue {
  return ProtoValue.create({
    kind: { $case: "nullValue", value: NullValue.NULL_VALUE },
  });
}

export class ValueHydrator {
  public constructor(
    private readonly service: FlowServiceClient,
    private readonly blobCache: BlobCache,
  ) {}

  public async hydrate(value: ProtoValue | undefined): Promise<ProtoValue> {
    if (value?.kind === undefined) {
      throw new TypeError("Value has no concrete kind");
    }
    const blobId = blobIdOf(value);
    if (blobId === undefined) {
      return value;
    }
    const cached = this.blobCache.get(blobId);
    if (cached !== undefined) {
      return ProtoValue.decode(cached);
    }
    const response = await unary<LoadBlobsResponse>((callback) =>
      this.service.loadBlobs({ values: [value] }, callback),
    );
    const hydrated = response.values[blobId];
    if (hydrated?.kind === undefined || blobIdOf(hydrated) !== undefined) {
      throw new TypeError(`Dex did not hydrate blob ${blobId}`);
    }
    this.blobCache.put(blobId, ProtoValue.encode(hydrated).finish());
    return hydrated;
  }

  public async hydrateAll(values: readonly (ProtoValue | undefined)[]): Promise<ProtoValue[]> {
    const hydrated: Array<ProtoValue | undefined> = new Array(values.length);
    const missing = new Map<string, { value: ProtoValue; indexes: number[] }>();
    for (let index = 0; index < values.length; index += 1) {
      const value = values[index];
      if (value?.kind === undefined) {
        throw new TypeError("Value has no concrete kind");
      }
      const blobId = blobIdOf(value);
      if (blobId === undefined) {
        hydrated[index] = value;
        continue;
      }
      const cached = this.blobCache.get(blobId);
      if (cached !== undefined) {
        hydrated[index] = ProtoValue.decode(cached);
        continue;
      }
      const pending = missing.get(blobId);
      if (pending === undefined) {
        missing.set(blobId, { value, indexes: [index] });
      } else {
        pending.indexes.push(index);
      }
    }
    if (missing.size > 0) {
      const response = await unary<LoadBlobsResponse>((callback) =>
        this.service.loadBlobs(
          { values: [...missing.values()].map((pending) => pending.value) },
          callback,
        ),
      );
      for (const [blobId, pending] of missing) {
        const value = response.values[blobId];
        if (value?.kind === undefined || blobIdOf(value) !== undefined) {
          throw new TypeError(`Dex did not hydrate blob ${blobId}`);
        }
        this.blobCache.put(blobId, ProtoValue.encode(value).finish());
        for (const index of pending.indexes) {
          hydrated[index] = value;
        }
      }
    }
    return hydrated.map((value) => {
      if (value === undefined) {
        throw new TypeError("Value hydration left an unresolved entry");
      }
      return value;
    });
  }
}

function objectValue(encoding: string, payload: Uint8Array): ProtoValue {
  return ProtoValue.create({
    kind: { $case: "objValue", value: { encoding, payload } },
  });
}

function toCodecValue(value: ProtoValue): CodecValue {
  const kind = value.kind;
  if (kind === undefined) {
    throw new TypeError("Value has no concrete kind");
  }
  switch (kind.$case) {
    case "stringValue":
      return { kind: "string", data: kind.value };
    case "boolValue":
      return { kind: "bool", data: kind.value };
    case "intValue":
      return { kind: "int64", data: kind.value };
    case "doubleValue":
      return { kind: "double", data: kind.value };
    case "objValue":
      if (kind.value.encoding === "rawbytes") {
        return { kind: "bytes", data: kind.value.payload };
      }
      if (kind.value.encoding === "json") {
        return { kind: "json", data: textDecoder.decode(kind.value.payload) };
      }
      throw new TypeError(`unsupported object encoding ${kind.value.encoding}`);
    case "internalBlobIdForStringValue":
    case "internalBlobIdForObjValue":
      throw new TypeError("blob-backed Value was not hydrated");
    case "nullValue":
      throw new TypeError("attribute deletion marker cannot be decoded");
  }
}

function blobIdOf(value: ProtoValue): string | undefined {
  if (
    value.kind?.$case === "internalBlobIdForStringValue" ||
    value.kind?.$case === "internalBlobIdForObjValue"
  ) {
    return value.kind.value;
  }
  return undefined;
}

function unary<Response>(
  invoke: (callback: (error: Error | null, response: Response) => void) => unknown,
): Promise<Response> {
  return new Promise((resolve, reject) => {
    invoke((error, response) => {
      if (error !== null) {
        reject(error);
        return;
      }
      resolve(response);
    });
  });
}
