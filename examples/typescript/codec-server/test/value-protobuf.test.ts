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

import assert from "node:assert/strict";
import { test } from "node:test";

import {
  decodeDexValue,
  DexValueFormatError,
  encodeDexValue,
  formatDexValueJson,
  measureDexValueJsonBytes,
  parseDexValueJson,
  type DexValue,
} from "../src/value-protobuf.ts";
import { assertSameDexValue, overflowingIntValuePlaintext, readGoldenVectors } from "./test-support.ts";

test("golden vector plaintexts decode to their expected Values and encode back byte for byte", () => {
  const vectors = readGoldenVectors();
  let checked = 0;
  for (const vectorCase of vectors.positive) {
    const plaintext = new Uint8Array(Buffer.from(vectorCase.expectedPlaintext!, "base64"));
    const expected = parseDexValueJson(vectorCase.expectedValue);
    assertSameDexValue(decodeDexValue(plaintext), expected, vectorCase.name);
    // The vectors README says not to compare NaN bits.
    if (!(expected.kind === "doubleValue" && Number.isNaN(expected.doubleValue))) {
      assert.deepEqual(Buffer.from(encodeDexValue(expected)), Buffer.from(plaintext), vectorCase.name);
    }
    assertSameDexValue(parseDexValueJson(JSON.parse(formatDexValueJson(expected))), expected, vectorCase.name);
    checked += 1;
  }
  assert.ok(checked >= 20, `only ${checked} positive vectors checked`);
});

test("Value JSON keeps int64 extremes, special doubles, and negative zero", () => {
  assert.equal(formatDexValueJson({ kind: "doubleValue", doubleValue: -0 }), `{"doubleValue":-0}`);
  assert.equal(formatDexValueJson({ kind: "doubleValue", doubleValue: Number.NaN }), `{"doubleValue":"NaN"}`);
  assert.equal(
    formatDexValueJson({ kind: "doubleValue", doubleValue: Number.NEGATIVE_INFINITY }),
    `{"doubleValue":"-Infinity"}`,
  );
  assert.equal(formatDexValueJson({ kind: "intValue", intValue: -(2n ** 63n) }), `{"intValue":"-9223372036854775808"}`);
  assert.equal(formatDexValueJson({ kind: "objValue", encoding: "", payload: new Uint8Array(0) }), `{"objValue":{}}`);
  assert.deepEqual(parseDexValueJson({ int_value: 42 }), { kind: "intValue", intValue: 42n });
  assert.deepEqual(parseDexValueJson({ string_value: "x" }), { kind: "stringValue", stringValue: "x" });
  assert.deepEqual(parseDexValueJson({ objValue: { encoding: "raw", payload: "AAEC_v8" } }), {
    kind: "objValue",
    encoding: "raw",
    payload: Uint8Array.from([0, 1, 2, 254, 255]),
  });
});

test("the measured JSON length equals the serialized length", () => {
  const values: DexValue[] = [
    { kind: "stringValue", stringValue: "" },
    { kind: "stringValue", stringValue: "\u0001\u001f\"\\\b\f\n\r\t\u007f plain" },
    { kind: "stringValue", stringValue: "Grüße, 世界 🌍  " },
    { kind: "stringValue", stringValue: "lone \ud800 high and \udc00 low" },
    { kind: "objValue", encoding: "", payload: new Uint8Array(0) },
    { kind: "objValue", encoding: "json\u0002", payload: new Uint8Array(0) },
    { kind: "objValue", encoding: "", payload: Uint8Array.from([1, 2, 3, 4]) },
    { kind: "objValue", encoding: "raw", payload: Uint8Array.from([0, 1, 2, 254, 255]) },
    { kind: "intValue", intValue: -(2n ** 63n) },
    { kind: "doubleValue", doubleValue: -0 },
    { kind: "doubleValue", doubleValue: Number.NaN },
    { kind: "doubleValue", doubleValue: 1.7976931348623157e308 },
    { kind: "boolValue", boolValue: false },
    { kind: "internalBlobIdForStringValue", blobId: "blob\u0003" },
  ];
  for (const vectorCase of readGoldenVectors().positive) {
    values.push(parseDexValueJson(vectorCase.expectedValue));
  }
  for (const value of values) {
    assert.equal(
      measureDexValueJsonBytes(value),
      Buffer.byteLength(formatDexValueJson(value), "utf8"),
      formatDexValueJson(value),
    );
  }
});

test("Value JSON rejects malformed input", () => {
  const malformed: unknown[] = [
    [],
    "hello",
    { stringValue: 1 },
    { stringValue: "a", intValue: "1" },
    { unknownValue: "a" },
    { intValue: "9223372036854775808" },
    { intValue: "1.5" },
    { intValue: 1.5 },
    { boolValue: "true" },
    { doubleValue: "fast" },
    { objValue: { encoding: "json", extra: 1 } },
    { objValue: { payload: "not base64!" } },
  ];
  for (const json of malformed) {
    assert.throws(() => parseDexValueJson(json), DexValueFormatError, JSON.stringify(json));
  }
});

test("Value protobuf rejects truncation, wrong wire types, and invalid UTF-8, and skips unknown fields", () => {
  assert.throws(() => decodeDexValue(Uint8Array.from([0x1a, 0x05, 0x61])), DexValueFormatError);
  assert.throws(() => decodeDexValue(Uint8Array.from([0x28])), DexValueFormatError);
  assert.throws(() => decodeDexValue(Uint8Array.from([0x1a, 0x01, 0xff])), DexValueFormatError);
  assert.throws(() => decodeDexValue(Uint8Array.from([0x18, 0x01])), DexValueFormatError);
  // A varint whose 10th byte sets bits above 63 overflows int64.
  assert.throws(() => decodeDexValue(overflowingIntValuePlaintext), DexValueFormatError);
  assert.throws(
    () => decodeDexValue(Uint8Array.from([0x28, ...new Array<number>(10).fill(0xff), 0x01])),
    DexValueFormatError,
  );
  assert.throws(
    () => decodeDexValue(Uint8Array.from([0x1a, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02])),
    DexValueFormatError,
  );
  assert.deepEqual(
    decodeDexValue(Uint8Array.from([0x28, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01])),
    { kind: "intValue", intValue: -(2n ** 63n) },
  );
  assert.deepEqual(
    decodeDexValue(Uint8Array.from([0x28, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01])),
    { kind: "intValue", intValue: -1n },
  );
  assert.deepEqual(
    decodeDexValue(Uint8Array.from([0x1a, 0x01, 0x61, 0x78, 0x05])),
    { kind: "stringValue", stringValue: "a" },
  );
  assert.deepEqual(
    decodeDexValue(Uint8Array.from([0x1a, 0x01, 0x61, 0x38, 0x01])),
    { kind: "boolValue", boolValue: true },
  );
});
