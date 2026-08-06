// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

export type WireKind = "string" | "bool" | "int64" | "double" | "bytes" | "json";

export type Value =
  | Readonly<{ kind: "string"; data: string }>
  | Readonly<{ kind: "bool"; data: boolean }>
  | Readonly<{ kind: "int64"; data: bigint }>
  | Readonly<{ kind: "double"; data: number }>
  | Readonly<{ kind: "bytes"; data: Uint8Array }>
  | Readonly<{ kind: "json"; data: string }>;

export interface Codec<T> {
  readonly typeName: string;
  readonly wireKind: WireKind;
  encode(value: T): Value;
  decode(value: Value): T;
}

export const stringCodec: Codec<string> = {
  typeName: "string",
  wireKind: "string",
  encode: (value) => ({ kind: "string", data: value }),
  decode: (value) => requireKind(value, "string").data,
};

export const booleanCodec: Codec<boolean> = {
  typeName: "boolean",
  wireKind: "bool",
  encode: (value) => ({ kind: "bool", data: value }),
  decode: (value) => requireKind(value, "bool").data,
};

export const int64Codec: Codec<bigint> = {
  typeName: "bigint",
  wireKind: "int64",
  encode: (value) => {
    if (value < -(2n ** 63n) || value > 2n ** 63n - 1n) {
      throw new RangeError(`${value} exceeds int64`);
    }
    return { kind: "int64", data: value };
  },
  decode: (value) => requireKind(value, "int64").data,
};

export const doubleCodec: Codec<number> = {
  typeName: "number",
  wireKind: "double",
  encode: (value) => {
    if (!Number.isFinite(value)) {
      throw new RangeError("non-finite numbers are unsupported");
    }
    return { kind: "double", data: value };
  },
  decode: (value) => requireKind(value, "double").data,
};

export const bytesCodec: Codec<Uint8Array> = {
  typeName: "Uint8Array",
  wireKind: "bytes",
  encode: (value) => ({ kind: "bytes", data: value.slice() }),
  decode: (value) => requireKind(value, "bytes").data.slice(),
};

export const voidCodec: Codec<void> = {
  typeName: "void",
  wireKind: "json",
  encode: () => ({ kind: "json", data: "null" }),
  decode: (value) => {
    if (requireKind(value, "json").data !== "null") {
      throw new TypeError("void requires JSON null");
    }
  },
};

export function jsonCodec<T>(options: {
  readonly typeName: string;
  readonly decode: (value: unknown) => T;
  readonly encode?: (value: T) => unknown;
}): Codec<T> {
  return {
    typeName: options.typeName,
    wireKind: "json",
    encode: (value) => {
      const data = JSON.stringify(options.encode?.(value) ?? value);
      if (data === undefined) {
        throw new TypeError("JSON codec cannot encode undefined");
      }
      return { kind: "json", data };
    },
    decode: (value) => options.decode(JSON.parse(requireKind(value, "json").data) as unknown),
  };
}

function requireKind<K extends WireKind>(value: Value, kind: K): Extract<Value, { kind: K }> {
  if (value.kind !== kind) {
    throw new TypeError(`expected ${kind}, got ${value.kind}`);
  }
  return value as Extract<Value, { kind: K }>;
}
