// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

/** agents/conformance, from dist/test/fixtures. */
export const conformanceSuiteDirectory = fileURLToPath(new URL("../../../../../conformance/", import.meta.url));

export interface GoldenValueCase {
  readonly name: string;
  readonly description: string;
  readonly wire: string;
  readonly canonical: string;
}

export interface GoldenValueDocument {
  readonly type: string;
  readonly wire_kind: "json" | "string" | "int64" | "bool";
  readonly description: string;
  readonly cases: readonly GoldenValueCase[];
}

export function readConformanceJson<Value>(relativePath: string): Value {
  return JSON.parse(readFileSync(`${conformanceSuiteDirectory}${relativePath}`, "utf8")) as Value;
}

export function readGoldenValueDocuments(): GoldenValueDocument[] {
  return readdirSync(`${conformanceSuiteDirectory}values`)
    .filter((name) => name.endsWith(".json"))
    .sort()
    .map((name) => readConformanceJson<GoldenValueDocument>(`values/${name}`));
}

/**
 * Serializes JSON in the suite's canonical form: object keys sorted, no whitespace, and every character
 * outside ASCII escaped as lowercase `\uXXXX`, which is the Python Dex SDK's JSON encoding.
 */
export function serializeCanonicalJson(value: unknown): string {
  if (Array.isArray(value)) {
    return `[${value.map(serializeCanonicalJson).join(",")}]`;
  }
  if (typeof value === "object" && value !== null) {
    const record = value as Readonly<Record<string, unknown>>;
    const keys = Object.keys(record).filter((key) => record[key] !== undefined).sort(compareByCodePoint);
    return `{${keys.map((key) => `${escapeNonAscii(JSON.stringify(key))}:${serializeCanonicalJson(record[key])}`).join(",")}}`;
  }
  const text = JSON.stringify(value);
  if (text === undefined) {
    throw new TypeError(`cannot serialize ${typeof value} as canonical JSON`);
  }
  return escapeNonAscii(text);
}

function escapeNonAscii(text: string): string {
  return text.replace(/[\u0080-￿]/g, (character) => `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`);
}

/** Orders strings by code point, as Python does; JavaScript's `<` puts characters above U+FFFF before U+E000. */
export function compareByCodePoint(left: string, right: string): number {
  const leftCodePoints = Array.from(left, (character) => character.codePointAt(0) ?? 0);
  const rightCodePoints = Array.from(right, (character) => character.codePointAt(0) ?? 0);
  for (let index = 0; index < Math.min(leftCodePoints.length, rightCodePoints.length); index += 1) {
    const difference = (leftCodePoints[index] ?? 0) - (rightCodePoints[index] ?? 0);
    if (difference !== 0) {
      return difference;
    }
  }
  return leftCodePoints.length - rightCodePoints.length;
}
