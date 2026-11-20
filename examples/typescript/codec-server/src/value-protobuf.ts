/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// dex.Value protobuf and proto3 JSON, hand-written because the SDK does not export Value.

/** One decoded dex.Value. Only the oneof case that is set is present. */
export type DexValue =
  | { readonly kind: "internalBlobIdForStringValue"; readonly blobId: string }
  | { readonly kind: "internalBlobIdForObjValue"; readonly blobId: string }
  | { readonly kind: "stringValue"; readonly stringValue: string }
  | { readonly kind: "objValue"; readonly encoding: string; readonly payload: Uint8Array }
  | { readonly kind: "intValue"; readonly intValue: bigint }
  | { readonly kind: "doubleValue"; readonly doubleValue: number }
  | { readonly kind: "boolValue"; readonly boolValue: boolean }
  | { readonly kind: "nullValue" }
  | { readonly kind: "unset" };

/** Thrown when bytes or JSON are not a valid dex.Value. */
export class DexValueFormatError extends Error {}

export const reservedPayloadEncodingPrefix = "dex/";

const minInt64 = -(2n ** 63n);
const maxInt64 = 2n ** 63n - 1n;
const strictUtf8Decoder = new TextDecoder("utf-8", { fatal: true });
const utf8Encoder = new TextEncoder();

const wireVarint = 0;
const wireFixed64 = 1;
const wireLengthDelimited = 2;
const wireFixed32 = 5;

/**
 * Explains why a Value is never encrypted (protos/payload-encryption.md section 2).
 * @returns The reason, or undefined for an encryptable Value.
 */
export function describeUnencryptableValue(value: DexValue): string | undefined {
  switch (value.kind) {
    case "stringValue":
    case "intValue":
    case "doubleValue":
    case "boolValue":
      return undefined;
    case "objValue":
      return value.encoding.startsWith(reservedPayloadEncodingPrefix)
        ? "object encodings starting with dex/ are reserved"
        : undefined;
    case "unset":
      return "value has no kind";
    default:
      return "null values and blob references are never encrypted";
  }
}

// ---------------------------------------------------------------------------
// Protobuf wire format

class ProtobufReader {
  private position = 0;
  private readonly bytes: Uint8Array;

  public constructor(bytes: Uint8Array) {
    this.bytes = bytes;
  }

  public atEnd(): boolean {
    return this.position >= this.bytes.length;
  }

  public readVarint(): bigint {
    let result = 0n;
    for (let shift = 0n; shift < 70n; shift += 7n) {
      if (this.position >= this.bytes.length) {
        throw new DexValueFormatError("truncated varint");
      }
      const byte = this.bytes[this.position++]!;
      // The 10th byte holds only bit 63; anything more overflows 64 bits.
      if (shift === 63n && byte > 0x01) {
        throw new DexValueFormatError("varint overflows 64 bits");
      }
      result |= BigInt(byte & 0x7f) << shift;
      if ((byte & 0x80) === 0) {
        return result;
      }
    }
    throw new DexValueFormatError("varint is longer than 10 bytes");
  }

  public readTag(): { fieldNumber: number; wireType: number } {
    const tag = this.readVarint();
    if (tag > 0xffff_ffffn) {
      throw new DexValueFormatError("tag is out of range");
    }
    const fieldNumber = Number(tag >> 3n);
    if (fieldNumber === 0) {
      throw new DexValueFormatError("field number 0 is invalid");
    }
    return { fieldNumber, wireType: Number(tag & 7n) };
  }

  public readLengthDelimited(): Uint8Array {
    const length = this.readVarint();
    if (length > BigInt(this.bytes.length - this.position)) {
      throw new DexValueFormatError("truncated length-delimited field");
    }
    const start = this.position;
    this.position += Number(length);
    return this.bytes.subarray(start, this.position);
  }

  public readFixed64(): DataView {
    if (this.bytes.length - this.position < 8) {
      throw new DexValueFormatError("truncated fixed64 field");
    }
    const view = new DataView(this.bytes.buffer, this.bytes.byteOffset + this.position, 8);
    this.position += 8;
    return view;
  }

  public skipField(wireType: number): void {
    switch (wireType) {
      case wireVarint:
        this.readVarint();
        return;
      case wireFixed64:
        this.readFixed64();
        return;
      case wireLengthDelimited:
        this.readLengthDelimited();
        return;
      case wireFixed32:
        if (this.bytes.length - this.position < 4) {
          throw new DexValueFormatError("truncated fixed32 field");
        }
        this.position += 4;
        return;
      default:
        throw new DexValueFormatError(`wire type ${wireType} is not supported`);
    }
  }
}

function requireWireType(actual: number, expected: number): void {
  if (actual !== expected) {
    throw new DexValueFormatError("a known field has the wrong wire type");
  }
}

function decodeUtf8(bytes: Uint8Array): string {
  try {
    return strictUtf8Decoder.decode(bytes);
  } catch {
    throw new DexValueFormatError("string field is not valid UTF-8");
  }
}

function toSignedInt64(unsigned: bigint): bigint {
  return BigInt.asIntN(64, unsigned);
}

/**
 * Parses a serialized dex.Value with proto3 semantics.
 * Unknown fields are skipped; the last oneof field wins.
 * @throws {DexValueFormatError} When the bytes are not a valid dex.Value.
 */
export function decodeDexValue(bytes: Uint8Array): DexValue {
  const reader = new ProtobufReader(bytes);
  let value: DexValue = { kind: "unset" };
  while (!reader.atEnd()) {
    const { fieldNumber, wireType } = reader.readTag();
    switch (fieldNumber) {
      case 1:
        requireWireType(wireType, wireLengthDelimited);
        value = { kind: "internalBlobIdForStringValue", blobId: decodeUtf8(reader.readLengthDelimited()) };
        break;
      case 2:
        requireWireType(wireType, wireLengthDelimited);
        value = { kind: "internalBlobIdForObjValue", blobId: decodeUtf8(reader.readLengthDelimited()) };
        break;
      case 3:
        requireWireType(wireType, wireLengthDelimited);
        value = { kind: "stringValue", stringValue: decodeUtf8(reader.readLengthDelimited()) };
        break;
      case 4: {
        requireWireType(wireType, wireLengthDelimited);
        const base = value.kind === "objValue" ? value : { encoding: "", payload: new Uint8Array(0) };
        value = decodeEncodedObject(reader.readLengthDelimited(), base.encoding, base.payload);
        break;
      }
      case 5:
        requireWireType(wireType, wireVarint);
        value = { kind: "intValue", intValue: toSignedInt64(reader.readVarint()) };
        break;
      case 6:
        requireWireType(wireType, wireFixed64);
        value = { kind: "doubleValue", doubleValue: reader.readFixed64().getFloat64(0, true) };
        break;
      case 7:
        requireWireType(wireType, wireVarint);
        value = { kind: "boolValue", boolValue: reader.readVarint() !== 0n };
        break;
      case 8:
        requireWireType(wireType, wireVarint);
        reader.readVarint();
        value = { kind: "nullValue" };
        break;
      default:
        reader.skipField(wireType);
    }
  }
  return value;
}

function decodeEncodedObject(bytes: Uint8Array, encoding: string, payload: Uint8Array): DexValue {
  const reader = new ProtobufReader(bytes);
  let mergedEncoding = encoding;
  let mergedPayload = payload;
  while (!reader.atEnd()) {
    const { fieldNumber, wireType } = reader.readTag();
    switch (fieldNumber) {
      case 1:
        requireWireType(wireType, wireLengthDelimited);
        mergedEncoding = decodeUtf8(reader.readLengthDelimited());
        break;
      case 2:
        requireWireType(wireType, wireLengthDelimited);
        mergedPayload = Uint8Array.from(reader.readLengthDelimited());
        break;
      default:
        reader.skipField(wireType);
    }
  }
  return { kind: "objValue", encoding: mergedEncoding, payload: mergedPayload };
}

class ProtobufWriter {
  private readonly chunks: Uint8Array[] = [];
  private length = 0;

  public writeVarint(value: bigint): void {
    let remaining = BigInt.asUintN(64, value);
    const bytes: number[] = [];
    do {
      let byte = Number(remaining & 0x7fn);
      remaining >>= 7n;
      if (remaining !== 0n) {
        byte |= 0x80;
      }
      bytes.push(byte);
    } while (remaining !== 0n);
    this.writeBytes(Uint8Array.from(bytes));
  }

  public writeTag(fieldNumber: number, wireType: number): void {
    this.writeVarint(BigInt((fieldNumber << 3) | wireType));
  }

  public writeLengthDelimited(fieldNumber: number, bytes: Uint8Array): void {
    this.writeTag(fieldNumber, wireLengthDelimited);
    this.writeVarint(BigInt(bytes.length));
    this.writeBytes(bytes);
  }

  public writeBytes(bytes: Uint8Array): void {
    this.chunks.push(bytes);
    this.length += bytes.length;
  }

  public finish(): Uint8Array {
    const output = new Uint8Array(this.length);
    let offset = 0;
    for (const chunk of this.chunks) {
      output.set(chunk, offset);
      offset += chunk.length;
    }
    return output;
  }
}

/**
 * Serializes a dex.Value. The oneof field is written even when it holds its default value.
 * @returns The protobuf bytes.
 */
export function encodeDexValue(value: DexValue): Uint8Array {
  const writer = new ProtobufWriter();
  switch (value.kind) {
    case "internalBlobIdForStringValue":
      writer.writeLengthDelimited(1, utf8Encoder.encode(value.blobId));
      break;
    case "internalBlobIdForObjValue":
      writer.writeLengthDelimited(2, utf8Encoder.encode(value.blobId));
      break;
    case "stringValue":
      writer.writeLengthDelimited(3, utf8Encoder.encode(value.stringValue));
      break;
    case "objValue": {
      const objectWriter = new ProtobufWriter();
      if (value.encoding !== "") {
        objectWriter.writeLengthDelimited(1, utf8Encoder.encode(value.encoding));
      }
      if (value.payload.length > 0) {
        objectWriter.writeLengthDelimited(2, value.payload);
      }
      writer.writeLengthDelimited(4, objectWriter.finish());
      break;
    }
    case "intValue":
      writer.writeTag(5, wireVarint);
      writer.writeVarint(value.intValue);
      break;
    case "doubleValue": {
      writer.writeTag(6, wireFixed64);
      const bytes = new Uint8Array(8);
      new DataView(bytes.buffer).setFloat64(0, value.doubleValue, true);
      writer.writeBytes(bytes);
      break;
    }
    case "boolValue":
      writer.writeTag(7, wireVarint);
      writer.writeVarint(value.boolValue ? 1n : 0n);
      break;
    case "nullValue":
      writer.writeTag(8, wireVarint);
      writer.writeVarint(0n);
      break;
    case "unset":
      break;
  }
  return writer.finish();
}

// ---------------------------------------------------------------------------
// Proto3 JSON mapping

/**
 * Writes a Value in the proto3 JSON mapping of dex.Value.
 * @returns JSON text. intValue is a decimal string; NaN, infinities, and -0 keep their meaning.
 */
export function formatDexValueJson(value: DexValue): string {
  switch (value.kind) {
    case "internalBlobIdForStringValue":
      return `{"internalBlobIdForStringValue":${JSON.stringify(value.blobId)}}`;
    case "internalBlobIdForObjValue":
      return `{"internalBlobIdForObjValue":${JSON.stringify(value.blobId)}}`;
    case "stringValue":
      return `{"stringValue":${JSON.stringify(value.stringValue)}}`;
    case "objValue": {
      const fields: string[] = [];
      if (value.encoding !== "") {
        fields.push(`"encoding":${JSON.stringify(value.encoding)}`);
      }
      if (value.payload.length > 0) {
        fields.push(`"payload":"${Buffer.from(value.payload).toString("base64")}"`);
      }
      return `{"objValue":{${fields.join(",")}}}`;
    }
    case "intValue":
      return `{"intValue":"${value.intValue.toString()}"}`;
    case "doubleValue":
      return `{"doubleValue":${formatDoubleJson(value.doubleValue)}}`;
    case "boolValue":
      return `{"boolValue":${value.boolValue ? "true" : "false"}}`;
    case "nullValue":
      return `{"nullValue":null}`;
    case "unset":
      return "{}";
  }
}

/**
 * Computes the UTF-8 byte length of formatDexValueJson(value) without building it.
 * Escaping can expand a string about six times, so callers check budgets first.
 * @returns The exact byte length of the JSON text.
 */
export function measureDexValueJsonBytes(value: DexValue): number {
  switch (value.kind) {
    case "internalBlobIdForStringValue":
      return `{"internalBlobIdForStringValue":}`.length + measureJsonStringBytes(value.blobId);
    case "internalBlobIdForObjValue":
      return `{"internalBlobIdForObjValue":}`.length + measureJsonStringBytes(value.blobId);
    case "stringValue":
      return `{"stringValue":}`.length + measureJsonStringBytes(value.stringValue);
    case "objValue": {
      const fieldBytes: number[] = [];
      if (value.encoding !== "") {
        fieldBytes.push(`"encoding":`.length + measureJsonStringBytes(value.encoding));
      }
      if (value.payload.length > 0) {
        fieldBytes.push(`"payload":""`.length + 4 * Math.ceil(value.payload.length / 3));
      }
      const separators = Math.max(0, fieldBytes.length - 1);
      return `{"objValue":{}}`.length + separators + fieldBytes.reduce((sum, bytes) => sum + bytes, 0);
    }
    default:
      // Numbers, booleans, and null have short, bounded JSON.
      return Buffer.byteLength(formatDexValueJson(value), "utf8");
  }
}

/** @returns The UTF-8 byte length of JSON.stringify(text), counting escapes. */
function measureJsonStringBytes(text: string): number {
  let bytes = 2;
  for (let index = 0; index < text.length; index += 1) {
    const code = text.charCodeAt(index);
    if (code === 0x22 || code === 0x5c || code === 0x08 || code === 0x09 || code === 0x0a || code === 0x0c || code === 0x0d) {
      bytes += 2;
    } else if (code < 0x20) {
      bytes += 6;
    } else if (code < 0x80) {
      bytes += 1;
    } else if (code < 0x800) {
      bytes += 2;
    } else if (code >= 0xd800 && code <= 0xdbff && isLowSurrogate(text.charCodeAt(index + 1))) {
      bytes += 4;
      index += 1;
    } else if (code >= 0xd800 && code <= 0xdfff) {
      // JSON.stringify escapes a lone surrogate as \uXXXX.
      bytes += 6;
    } else {
      bytes += 3;
    }
  }
  return bytes;
}

function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

function formatDoubleJson(double: number): string {
  if (Number.isNaN(double)) {
    return `"NaN"`;
  }
  if (double === Number.POSITIVE_INFINITY) {
    return `"Infinity"`;
  }
  if (double === Number.NEGATIVE_INFINITY) {
    return `"-Infinity"`;
  }
  if (Object.is(double, -0)) {
    return "-0";
  }
  return String(double);
}

const valueJsonFieldKinds: ReadonlyMap<string, DexValue["kind"]> = new Map([
  ["internalBlobIdForStringValue", "internalBlobIdForStringValue"],
  ["internal_blob_id_for_string_value", "internalBlobIdForStringValue"],
  ["internalBlobIdForObjValue", "internalBlobIdForObjValue"],
  ["internal_blob_id_for_obj_value", "internalBlobIdForObjValue"],
  ["stringValue", "stringValue"],
  ["string_value", "stringValue"],
  ["objValue", "objValue"],
  ["obj_value", "objValue"],
  ["intValue", "intValue"],
  ["int_value", "intValue"],
  ["doubleValue", "doubleValue"],
  ["double_value", "doubleValue"],
  ["boolValue", "boolValue"],
  ["bool_value", "boolValue"],
  ["nullValue", "nullValue"],
  ["null_value", "nullValue"],
]);

/**
 * Reads proto3 JSON of dex.Value, with lowerCamelCase or proto field names.
 * @throws {DexValueFormatError} When the JSON is not a dex.Value.
 */
export function parseDexValueJson(json: unknown): DexValue {
  if (!isJsonObject(json)) {
    throw new DexValueFormatError("a Value must be a JSON object");
  }
  let value: DexValue = { kind: "unset" };
  for (const [fieldName, fieldValue] of Object.entries(json)) {
    const kind = valueJsonFieldKinds.get(fieldName);
    if (kind === undefined) {
      throw new DexValueFormatError("a Value has an unknown field");
    }
    if (fieldValue === null && kind !== "nullValue") {
      continue;
    }
    if (value.kind !== "unset") {
      throw new DexValueFormatError("a Value sets more than one kind");
    }
    value = parseValueJsonField(kind, fieldValue);
  }
  return value;
}

function parseValueJsonField(kind: DexValue["kind"], fieldValue: unknown): DexValue {
  switch (kind) {
    case "internalBlobIdForStringValue":
    case "internalBlobIdForObjValue":
      return { kind, blobId: requireJsonString(fieldValue) };
    case "stringValue":
      return { kind, stringValue: requireJsonString(fieldValue) };
    case "objValue":
      return parseEncodedObjectJson(fieldValue);
    case "intValue":
      return { kind, intValue: parseInt64Json(fieldValue) };
    case "doubleValue":
      return { kind, doubleValue: parseDoubleJson(fieldValue) };
    case "boolValue":
      if (typeof fieldValue !== "boolean") {
        throw new DexValueFormatError("boolValue must be true or false");
      }
      return { kind, boolValue: fieldValue };
    case "nullValue":
      if (fieldValue !== null && fieldValue !== "NULL_VALUE" && fieldValue !== 0) {
        throw new DexValueFormatError("nullValue must be null");
      }
      return { kind };
    case "unset":
      throw new DexValueFormatError("a Value has an unknown field");
  }
}

function parseEncodedObjectJson(json: unknown): DexValue {
  if (!isJsonObject(json)) {
    throw new DexValueFormatError("objValue must be a JSON object");
  }
  let encoding = "";
  let payload: Uint8Array = new Uint8Array(0);
  for (const [fieldName, fieldValue] of Object.entries(json)) {
    if (fieldValue === null) {
      continue;
    }
    if (fieldName === "encoding") {
      encoding = requireJsonString(fieldValue);
    } else if (fieldName === "payload") {
      payload = parseBase64Json(requireJsonString(fieldValue));
    } else {
      throw new DexValueFormatError("objValue has an unknown field");
    }
  }
  return { kind: "objValue", encoding, payload };
}

// Proto3 JSON readers accept standard or URL-safe base64, with or without padding.
const base64JsonPattern = /^(?:[A-Za-z0-9+/]|[A-Za-z0-9\-_])*={0,2}$/;

function parseBase64Json(text: string): Uint8Array {
  const unpadded = text.replace(/=+$/, "");
  if (!base64JsonPattern.test(text) || unpadded.length % 4 === 1) {
    throw new DexValueFormatError("objValue.payload is not base64");
  }
  if (text.length !== unpadded.length && text.length % 4 !== 0) {
    throw new DexValueFormatError("objValue.payload has invalid padding");
  }
  const standard = unpadded.replace(/-/g, "+").replace(/_/g, "/");
  return new Uint8Array(Buffer.from(standard, "base64"));
}

const decimalIntegerPattern = /^-?(?:0|[1-9][0-9]*)$/;

function parseInt64Json(json: unknown): bigint {
  let parsed: bigint;
  if (typeof json === "string" && decimalIntegerPattern.test(json)) {
    parsed = BigInt(json);
  } else if (typeof json === "number" && Number.isSafeInteger(json)) {
    parsed = BigInt(json);
  } else {
    throw new DexValueFormatError("intValue must be a decimal integer string");
  }
  if (parsed < minInt64 || parsed > maxInt64) {
    throw new DexValueFormatError("intValue is out of the int64 range");
  }
  return parsed;
}

const decimalNumberPattern = /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$/;

function parseDoubleJson(json: unknown): number {
  if (typeof json === "number") {
    return json;
  }
  if (json === "NaN") {
    return Number.NaN;
  }
  if (json === "Infinity") {
    return Number.POSITIVE_INFINITY;
  }
  if (json === "-Infinity") {
    return Number.NEGATIVE_INFINITY;
  }
  if (typeof json === "string" && decimalNumberPattern.test(json)) {
    const parsed = Number(json);
    if (Number.isFinite(parsed)) {
      return parsed;
    }
  }
  throw new DexValueFormatError("doubleValue must be a number, \"NaN\", \"Infinity\", or \"-Infinity\"");
}

function requireJsonString(json: unknown): string {
  if (typeof json !== "string") {
    throw new DexValueFormatError("expected a JSON string");
  }
  return json;
}

function isJsonObject(json: unknown): json is Record<string, unknown> {
  return typeof json === "object" && json !== null && !Array.isArray(json);
}
