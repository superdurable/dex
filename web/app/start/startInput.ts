// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2StartInputSchema } from '@superdurable/flow-definition-renderer';

export interface StartInputDraft {
  mode: 'value' | 'null' | 'omitted';
  scalar: string;
  booleanValue: boolean;
  fields: Record<string, StartInputDraft>;
  items: StartInputDraft[];
  entries: Array<{ id: number; key: string; value: StartInputDraft }>;
}

export interface StartInputError {
  path: string;
  message: string;
}

export interface SerializedStartInput {
  json: string;
  errors: StartInputError[];
}

let nextMapEntryID = 1;

export function createStartInputDraft(
  schema: FlowV2StartInputSchema,
  omitted = false,
): StartInputDraft {
  const fields: Record<string, StartInputDraft> = {};
  for (const field of schema.fields ?? []) {
    fields[field.name] = createStartInputDraft(field.schema, !field.required);
  }
  const items = schema.kind === 'array' && schema.fixedLength !== undefined
    ? Array.from({ length: schema.fixedLength }, () => createStartInputDraft(requiredSchema(schema.items)))
    : [];
  return {
    mode: omitted ? 'omitted' : schema.kind === 'null' ? 'null' : 'value',
    scalar: schema.enumValues?.[0]?.value.toString() ?? '',
    booleanValue: false,
    fields,
    items,
    entries: [],
  };
}

export function createMapEntry(schema: FlowV2StartInputSchema) {
  return { id: nextMapEntryID++, key: '', value: createStartInputDraft(requiredSchema(schema.values)) };
}

export function serializeStartInput(
  schema: FlowV2StartInputSchema,
  draft: StartInputDraft,
): SerializedStartInput {
  const errors: StartInputError[] = [];
  const json = serializeValue(schema, draft, '$', errors);
  return { json: json ?? 'null', errors };
}

function serializeValue(
  schema: FlowV2StartInputSchema,
  draft: StartInputDraft,
  path: string,
  errors: StartInputError[],
): string | undefined {
  if (draft.mode === 'omitted') return undefined;
  if (draft.mode === 'null' || schema.kind === 'null') {
    if (schema.kind !== 'null' && !schema.nullable) {
      errors.push({ path, message: 'This field cannot be null.' });
    }
    return 'null';
  }
  switch (schema.kind) {
    case 'string':
      validateString(schema, draft.scalar, path, errors);
      return JSON.stringify(draft.scalar);
    case 'integer':
      validateInteger(schema, draft.scalar, path, errors);
      return draft.scalar || '0';
    case 'number':
      if (!/^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$/.test(draft.scalar) ||
          !Number.isFinite(Number(draft.scalar))) {
        errors.push({ path, message: 'Enter a finite number.' });
      }
      return draft.scalar || '0';
    case 'boolean':
      return draft.booleanValue ? 'true' : 'false';
    case 'object': {
      const members: string[] = [];
      for (const field of schema.fields ?? []) {
        const fieldDraft = draft.fields[field.name];
        if (!fieldDraft) {
          if (field.required) errors.push({ path: `${path}.${field.name}`, message: 'This field is required.' });
          continue;
        }
        const value = serializeValue(field.schema, fieldDraft, `${path}.${field.name}`, errors);
        if (value !== undefined) members.push(`${JSON.stringify(field.name)}:${value}`);
        if (field.required && value === undefined) {
          errors.push({ path: `${path}.${field.name}`, message: 'This field is required.' });
        }
      }
      return `{${members.join(',')}}`;
    }
    case 'array': {
      if (schema.fixedLength !== undefined && draft.items.length !== schema.fixedLength) {
        errors.push({ path, message: `Keep exactly ${schema.fixedLength} items.` });
      }
      const itemSchema = requiredSchema(schema.items);
      return `[${draft.items.map((item, index) =>
        serializeValue(itemSchema, item, `${path}[${index}]`, errors) ?? 'null').join(',')}]`;
    }
    case 'map': {
      const keys = new Set<string>();
      const members = draft.entries.map((entry) => {
        const entryPath = `${path}[${JSON.stringify(entry.key)}]`;
        if (entry.key === '') errors.push({ path, message: 'Map keys cannot be empty.' });
        if (keys.has(entry.key)) errors.push({ path, message: `Map key ${JSON.stringify(entry.key)} is duplicated.` });
        keys.add(entry.key);
        const value = serializeValue(requiredSchema(schema.values), entry.value, entryPath, errors) ?? 'null';
        return `${JSON.stringify(entry.key)}:${value}`;
      });
      return `{${members.join(',')}}`;
    }
    default:
      errors.push({ path, message: `Unsupported input kind ${(schema as { kind: string }).kind}.` });
      return 'null';
  }
}

function validateString(
  schema: FlowV2StartInputSchema,
  value: string,
  path: string,
  errors: StartInputError[],
) {
  if (schema.enumValues && !schema.enumValues.some((option) => option.value === value)) {
    errors.push({ path, message: 'Choose one of the available values.' });
  }
  if (schema.format === 'date-time' && !isRFC3339(value)) {
    errors.push({ path, message: 'Enter a valid RFC3339 date and time.' });
  }
}

function validateInteger(
  schema: FlowV2StartInputSchema,
  value: string,
  path: string,
  errors: StartInputError[],
) {
  if (!/^-?(?:0|[1-9][0-9]*)$/.test(value)) {
    errors.push({ path, message: 'Enter a whole number.' });
    return;
  }
  const integer = BigInt(value);
  if ((schema.minimum !== undefined && integer < BigInt(schema.minimum)) ||
      (schema.maximum !== undefined && integer > BigInt(schema.maximum))) {
    errors.push({ path, message: `Enter a value from ${schema.minimum} to ${schema.maximum}.` });
  }
  if (schema.enumValues && !schema.enumValues.some((option) => option.value.toString() === value)) {
    errors.push({ path, message: 'Choose one of the available values.' });
  }
}

function isRFC3339(value: string): boolean {
  return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(value) &&
    !Number.isNaN(Date.parse(value));
}

function requiredSchema(schema: FlowV2StartInputSchema | undefined): FlowV2StartInputSchema {
  if (!schema) throw new Error('Start input schema is incomplete');
  return schema;
}
