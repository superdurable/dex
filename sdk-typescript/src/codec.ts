// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

/** Identifies the protocol representation carried by an encoded {@link Value}. */
export type WireKind = "string" | "bool" | "int64" | "double" | "bytes" | "json";

/** Contains one validated SDK value before protocol mapping. */
export type Value =
  | Readonly<{
      /** String wire discriminator. */
      kind: "string";
      /** Valid UTF-8 application text. */
      data: string;
    }>
  | Readonly<{
      /** Boolean wire discriminator. */
      kind: "bool";
      /** Boolean scalar payload. */
      data: boolean;
    }>
  | Readonly<{
      /** Signed-integer wire discriminator. */
      kind: "int64";
      /** Signed 64-bit integer payload. */
      data: bigint;
    }>
  | Readonly<{
      /** Floating-point wire discriminator. */
      kind: "double";
      /** Finite IEEE-754 number payload. */
      data: number;
    }>
  | Readonly<{
      /** Raw-bytes wire discriminator. */
      kind: "bytes";
      /** Uninterpreted byte payload. */
      data: Uint8Array;
    }>
  | Readonly<{
      /** JSON wire discriminator. */
      kind: "json";
      /** Valid JSON document text. */
      data: string;
    }>;

/**
 * Converts between an application type and one Dex wire representation.
 * Implementations should be deterministic so durable replay sees identical values.
 *
 * @example
 * ```ts
 * type Order = { id: string };
 * const orderCodec = jsonCodec<Order>({
 *   typeName: "Order",
 *   decode(value) {
 *     if (typeof value !== "object" || value === null ||
 *         !("id" in value) || typeof value.id !== "string") {
 *       throw new TypeError("invalid Order");
 *     }
 *     return { id: value.id };
 *   },
 * });
 * const encoded = orderCodec.encode({ id: "order-42" });
 * ```
 * @typeParam T - Application value type encoded by this codec.
 */
export interface Codec<T> {
  /** Stable application-facing name used in mapping errors. */
  readonly typeName: string;
  /** Wire kind normally emitted and accepted by the codec. */
  readonly wireKind: WireKind;
  /**
   * Encodes one application value.
   * @param value - Typed value to encode.
   * @returns A validated Dex Value.
   */
  encode(value: T): Value;
  /**
   * Decodes one protocol value.
   * @param value - Value to validate and decode.
   * @returns The decoded application value.
   */
  decode(value: Value): T;
}

/** Built-in UTF-8 string codec. */
export const stringCodec: Codec<string> = {
  typeName: "string",
  wireKind: "string",
  encode: (value) => ({ kind: "string", data: value }),
  decode: (value) => requireKind(value, "string").data,
};

/** Built-in strict Boolean codec. */
export const booleanCodec: Codec<boolean> = {
  typeName: "boolean",
  wireKind: "bool",
  encode: (value) => ({ kind: "bool", data: value }),
  decode: (value) => requireKind(value, "bool").data,
};

/** Built-in signed 64-bit bigint codec. */
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

/** Built-in finite IEEE-754 number codec. */
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

/** Built-in byte-array codec that copies values in both directions. */
export const bytesCodec: Codec<Uint8Array> = {
  typeName: "Uint8Array",
  wireKind: "bytes",
  encode: (value) => ({ kind: "bytes", data: value.slice() }),
  decode: (value) => requireKind(value, "bytes").data.slice(),
};

/** Built-in void codec represented as JSON `null`. */
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

/**
 * Wraps a codec so JSON `null` represents `undefined`.
 * @typeParam T - Defined application value type.
 * @param codec - Codec used for defined values.
 * @returns A codec accepting the original type or `undefined`.
 */
export function optionalCodec<T>(codec: Codec<T>): Codec<T | undefined> {
  return {
    typeName: `${codec.typeName} | undefined`,
    wireKind: "json",
    encode: (value) => value === undefined ? { kind: "json", data: "null" } : codec.encode(value),
    decode: (value) => value.kind === "json" && value.data === "null" ? undefined : codec.decode(value),
  };
}

/**
 * Creates a deterministic JSON codec with application conversion hooks.
 *
 * JSON.stringify semantics apply; `undefined`, cycles, and unsupported bigint values
 * fail during encoding. The decoder receives parsed, untrusted JSON data.
 *
 * @typeParam T - Application value type.
 * @param options - Stable name plus JSON conversion hooks.
 * @returns A JSON-wire codec for `T`.
 * @throws {@link TypeError} when encoding produces `undefined`.
 */
export function jsonCodec<T>(options: {
  /** Stable type name used in mapping errors. */
  readonly typeName: string;
  /** Validates and converts parsed JSON-compatible data. */
  readonly decode: (value: unknown) => T;
  /** Converts an application value to JSON-compatible data; identity by default. */
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
