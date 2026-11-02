// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Checks the JSON Schema subset the Python core checks, with the same model-visible messages.

type JsonSchema = Readonly<Record<string, unknown>>;

/** Returns the first way `value` breaks the schema, such as `arguments.order_id must be string`, or `undefined`. */
export function findJsonSchemaViolation(value: unknown, schema: JsonSchema, path: string): string | undefined {
  const anyOf = schema["anyOf"];
  const isNoAnyOfOptionMatched =
    Array.isArray(anyOf) &&
    anyOf.every((option) => findJsonSchemaViolation(value, option as JsonSchema, path) !== undefined);
  if (isNoAnyOfOptionMatched) {
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
  if (Array.isArray(allowedValues) && !allowedValues.some((allowed) => isPythonEqual(allowed, value))) {
    return `${path} must be one of ${serializeJsonWithSpacedSeparators(allowedValues)}`;
  }
  if (typeof value === "object" && value !== null && !Array.isArray(value)) {
    return findObjectViolation(value as Readonly<Record<string, unknown>>, schema, path);
  }
  const items = schema["items"];
  if (Array.isArray(value) && typeof items === "object" && items !== null) {
    for (const [index, item] of value.entries()) {
      const violation = findJsonSchemaViolation(item, items as JsonSchema, `${path}[${index}]`);
      if (violation !== undefined) {
        return violation;
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

/**
 * Serializes JSON with `", "` and `": "` separators and unescaped non-ASCII text, the format of Python's
 * `json.dumps`, so model-visible text matches the Python core.
 */
export function serializeJsonWithSpacedSeparators(value: unknown): string {
  if (Array.isArray(value)) {
    return `[${value.map((item) => serializeJsonWithSpacedSeparators(item === undefined ? null : item)).join(", ")}]`;
  }
  if (typeof value === "object" && value !== null) {
    const record = typeof (value as { toJSON?: unknown }).toJSON === "function"
      ? ((value as { toJSON: () => unknown }).toJSON() as Readonly<Record<string, unknown>>)
      : (value as Readonly<Record<string, unknown>>);
    if (typeof record !== "object" || record === null) {
      return serializeJsonWithSpacedSeparators(record);
    }
    const entries = Object.entries(record).filter(([, nestedValue]) => nestedValue !== undefined);
    return `{${entries
      .map(([key, nestedValue]) => `${JSON.stringify(key)}: ${serializeJsonWithSpacedSeparators(nestedValue)}`)
      .join(", ")}}`;
  }
  const text = JSON.stringify(value);
  if (text === undefined) {
    throw new TypeError(`a ${typeof value} value cannot be serialized as JSON`);
  }
  return text;
}

function findObjectViolation(
  value: Readonly<Record<string, unknown>>,
  schema: JsonSchema,
  path: string,
): string | undefined {
  const properties = (schema["properties"] ?? {}) as Readonly<Record<string, JsonSchema>>;
  for (const requiredName of (schema["required"] ?? []) as readonly string[]) {
    if (!Object.hasOwn(value, requiredName)) {
      return `${path}.${requiredName} is required`;
    }
  }
  const additionalProperties = schema["additionalProperties"] ?? true;
  for (const [name, propertyValue] of Object.entries(value)) {
    let violation: string | undefined;
    if (Object.hasOwn(properties, name)) {
      violation = findJsonSchemaViolation(propertyValue, properties[name] ?? {}, `${path}.${name}`);
    } else if (additionalProperties === false) {
      violation = `${path}.${name} is not allowed`;
    } else if (typeof additionalProperties === "object" && additionalProperties !== null) {
      violation = findJsonSchemaViolation(propertyValue, additionalProperties as JsonSchema, `${path}.${name}`);
    }
    if (violation !== undefined) {
      return violation;
    }
  }
  return undefined;
}

/** Python's `==` over JSON values, which the Python core's `enum` check uses: `true == 1` and `1 == 1.0`. */
function isPythonEqual(left: unknown, right: unknown): boolean {
  const isNumeric = (value: unknown): value is number | boolean => typeof value === "number" || typeof value === "boolean";
  if (isNumeric(left) || isNumeric(right)) {
    return isNumeric(left) && isNumeric(right) && Number(left) === Number(right);
  }
  if (Array.isArray(left) || Array.isArray(right)) {
    return (
      Array.isArray(left) &&
      Array.isArray(right) &&
      left.length === right.length &&
      left.every((item, index) => isPythonEqual(item, right[index]))
    );
  }
  if (typeof left === "object" && left !== null && typeof right === "object" && right !== null) {
    const leftRecord = left as Readonly<Record<string, unknown>>;
    const rightRecord = right as Readonly<Record<string, unknown>>;
    const leftKeys = Object.keys(leftRecord);
    return (
      leftKeys.length === Object.keys(rightRecord).length &&
      leftKeys.every((key) => Object.hasOwn(rightRecord, key) && isPythonEqual(leftRecord[key], rightRecord[key]))
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
      return typeof value === "object" && value !== null && !Array.isArray(value);
    case "number":
      return typeof value === "number";
    case "integer":
      return typeof value === "number" && Number.isInteger(value);
    default:
      throw new TypeError(`unsupported JSON Schema type ${type}`);
  }
}
