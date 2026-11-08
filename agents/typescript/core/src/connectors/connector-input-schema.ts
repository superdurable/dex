// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Checks the JSON Schema subset of Connector `inputSchema` values with the Python core's messages.

import { serializeJsonWithSpacedSeparators } from "../stepwise/json-schema-validation.js";

type JsonSchema = Readonly<Record<string, unknown>>;

// Keywords that constrain a value; an unlisted keyword refuses the tool, so nothing is unchecked.
const checkedKeywords: ReadonlySet<string> = new Set([
  "type",
  "properties",
  "required",
  "additionalProperties",
  "items",
  "enum",
  "anyOf",
  "$ref",
  "$defs",
  "minimum",
  "maximum",
]);
// Annotations: format is an annotation in draft 2020-12, and the operation's validator checks it.
const annotationKeywords: ReadonlySet<string> = new Set([
  "description",
  "title",
  "format",
  "contentEncoding",
  "default",
  "$schema",
  "$id",
  "examples",
]);
const jsonTypes: ReadonlySet<string> = new Set(["string", "boolean", "null", "array", "object", "number", "integer"]);
const definitionsPrefix = "#/$defs/";

/**
 * Throws when a schema uses a keyword the agent core cannot enforce.
 * @param schema - A Connector operation's `inputSchema`.
 * @throws {TypeError} When the schema is not an object schema, uses an unknown keyword or type, or has a `$ref`
 * outside `#/$defs/`.
 */
export function checkConnectorInputSchema(schema: JsonSchema): void {
  if (schema["type"] !== "object") {
    throw new TypeError("inputSchema must have type object");
  }
  const definitions = schema["$defs"] ?? {};
  if (!isRecord(definitions)) {
    throw new TypeError("inputSchema $defs must be an object");
  }
  checkNode(schema, definitions, "inputSchema");
}

function checkNode(node: unknown, definitions: JsonSchema, path: string): void {
  if (!isRecord(node)) {
    throw new TypeError(`${path} must be a schema object`);
  }
  for (const [keyword, value] of Object.entries(node)) {
    if (annotationKeywords.has(keyword)) {
      continue;
    }
    if (!checkedKeywords.has(keyword)) {
      throw new TypeError(`${path} uses ${keyword}, which agent tools do not check`);
    }
    if (keyword === "type") {
      const names = typeof value === "string" ? [value] : value;
      if (!Array.isArray(names) || names.some((name) => typeof name !== "string" || !jsonTypes.has(name))) {
        throw new TypeError(`${path}.type is not a JSON type`);
      }
    } else if (keyword === "properties" || keyword === "$defs") {
      if (!isRecord(value)) {
        throw new TypeError(`${path}.${keyword} must be an object`);
      }
      for (const [name, child] of Object.entries(value)) {
        checkNode(child, definitions, `${path}.${keyword}.${name}`);
      }
    } else if (keyword === "items" || (keyword === "additionalProperties" && typeof value !== "boolean")) {
      checkNode(value, definitions, `${path}.${keyword}`);
    } else if (keyword === "anyOf") {
      if (!Array.isArray(value) || value.length === 0) {
        throw new TypeError(`${path}.anyOf must be a non-empty array`);
      }
      value.forEach((child, index) => checkNode(child, definitions, `${path}.anyOf[${index}]`));
    } else if (keyword === "$ref") {
      if (typeof value !== "string" || !value.startsWith(definitionsPrefix)) {
        throw new TypeError(`${path}.$ref must point into #/$defs/`);
      }
      if (!Object.hasOwn(definitions, value.slice(definitionsPrefix.length))) {
        throw new TypeError(`${path}.$ref names a missing definition`);
      }
    } else if (keyword === "required" && (!Array.isArray(value) || value.some((name) => typeof name !== "string"))) {
      throw new TypeError(`${path}.required must be an array of names`);
    } else if (keyword === "enum" && !Array.isArray(value)) {
      throw new TypeError(`${path}.enum must be an array`);
    }
  }
}

/**
 * Returns the first way the arguments break the schema, such as `arguments.amount must be integer`, or
 * `undefined`. The texts match the Python core.
 * @param value - The decoded JSON arguments.
 * @param schema - A schema that {@link checkConnectorInputSchema} accepted.
 * @returns The violation text, or `undefined`.
 */
export function findConnectorInputViolation(value: unknown, schema: JsonSchema): string | undefined {
  const definitions = schema["$defs"];
  return findViolation(value, schema, isRecord(definitions) ? definitions : {}, "arguments");
}

function findViolation(value: unknown, schema: JsonSchema, definitions: JsonSchema, path: string): string | undefined {
  const reference = schema["$ref"];
  if (typeof reference === "string") {
    const problem = findViolation(
      value,
      definitions[reference.slice(definitionsPrefix.length)] as JsonSchema,
      definitions,
      path,
    );
    if (problem !== undefined) {
      return problem;
    }
  }
  const anyOf = schema["anyOf"];
  if (
    Array.isArray(anyOf) &&
    anyOf.every((option) => findViolation(value, option as JsonSchema, definitions, path) !== undefined)
  ) {
    return `${path} matches none of the allowed types`;
  }
  const expected = schema["type"];
  if (expected !== undefined) {
    const expectedTypes = (typeof expected === "string" ? [expected] : (expected as unknown[])).map(String);
    if (!expectedTypes.some((type) => matchesJsonType(type, value))) {
      return `${path} must be ${expectedTypes.join(" or ")}`;
    }
  }
  const allowedValues = schema["enum"];
  if (Array.isArray(allowedValues) && !allowedValues.some((allowed) => isJsonEqual(value, allowed))) {
    return `${path} must be one of ${serializeJsonWithSpacedSeparators(allowedValues)}`;
  }
  if (isRecord(value)) {
    return findObjectViolation(value, schema, definitions, path);
  }
  const items = schema["items"];
  if (Array.isArray(value) && isRecord(items)) {
    for (const [index, item] of value.entries()) {
      const problem = findViolation(item, items, definitions, `${path}[${index}]`);
      if (problem !== undefined) {
        return problem;
      }
    }
  }
  if (typeof value === "number") {
    if (typeof schema["minimum"] === "number" && value < schema["minimum"]) {
      return `${path} must be at least ${schema["minimum"]}`;
    }
    if (typeof schema["maximum"] === "number" && value > schema["maximum"]) {
      return `${path} must be at most ${schema["maximum"]}`;
    }
  }
  return undefined;
}

function findObjectViolation(
  value: Readonly<Record<string, unknown>>,
  schema: JsonSchema,
  definitions: JsonSchema,
  path: string,
): string | undefined {
  const properties = (schema["properties"] ?? {}) as Readonly<Record<string, JsonSchema>>;
  for (const name of (schema["required"] ?? []) as readonly string[]) {
    if (!Object.hasOwn(value, name)) {
      return `${path}.${name} is required`;
    }
  }
  const additional = schema["additionalProperties"] ?? true;
  for (const name of Object.keys(value).sort(compareCodePoints)) {
    const item = value[name];
    let problem: string | undefined;
    if (Object.hasOwn(properties, name)) {
      problem = findViolation(item, properties[name] ?? {}, definitions, `${path}.${name}`);
    } else if (additional === false) {
      problem = `${path}.${name} is not allowed`;
    } else if (isRecord(additional)) {
      problem = findViolation(item, additional, definitions, `${path}.${name}`);
    }
    if (problem !== undefined) {
      return problem;
    }
  }
  return undefined;
}

/** JSON equality: booleans never equal numbers, and `1` equals `1.0`. */
function isJsonEqual(left: unknown, right: unknown): boolean {
  if (Array.isArray(left) || Array.isArray(right)) {
    return (
      Array.isArray(left) &&
      Array.isArray(right) &&
      left.length === right.length &&
      left.every((item, index) => isJsonEqual(item, right[index]))
    );
  }
  if (isRecord(left) && isRecord(right)) {
    const leftKeys = Object.keys(left);
    return (
      leftKeys.length === Object.keys(right).length &&
      leftKeys.every((key) => Object.hasOwn(right, key) && isJsonEqual(left[key], right[key]))
    );
  }
  return left === right;
}

function matchesJsonType(type: string, value: unknown): boolean {
  switch (type) {
    case "string":
      return typeof value === "string";
    case "boolean":
      return typeof value === "boolean";
    case "null":
      return value === null;
    case "array":
      return Array.isArray(value);
    case "object":
      return isRecord(value);
    case "number":
      return typeof value === "number";
    case "integer":
      return typeof value === "number" && Number.isInteger(value);
    default:
      throw new TypeError(`unsupported JSON Schema type ${type}`);
  }
}

/** Orders keys by code point, as Python's `sorted` does. */
export function compareCodePoints(left: string, right: string): number {
  return left < right ? -1 : left > right ? 1 : 0;
}

function isRecord(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
