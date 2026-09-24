// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { FlowV2StartInputSchema } from '@superdurable/flow-definition-renderer';
import { createMapEntry, createStartInputDraft, serializeStartInput } from './startInput';

const schema: FlowV2StartInputSchema = {
  kind: 'object',
  fields: [
    { name: 'count', required: true, schema: { kind: 'integer', minimum: '0', maximum: '18446744073709551615' } },
    { name: 'note', required: false, schema: { kind: 'string', nullable: true } },
    { name: 'labels', required: true, schema: { kind: 'map', values: { kind: 'string' } } },
  ],
};

describe('Start input serialization', () => {
  it('preserves exact integers and omitted optional fields', () => {
    const draft = createStartInputDraft(schema);
    draft.fields.count.scalar = '18446744073709551615';
    const result = serializeStartInput(schema, draft);
    expect(result.errors).toEqual([]);
    expect(result.json).toBe('{"count":18446744073709551615,"labels":{}}');
  });

  it('serializes explicit null and reports duplicate map keys by path', () => {
    const draft = createStartInputDraft(schema);
    draft.fields.count.scalar = '1';
    draft.fields.note.mode = 'null';
    const labels = schema.fields?.[2].schema;
    if (!labels) throw new Error('labels schema missing');
    draft.fields.labels.entries = [createMapEntry(labels), createMapEntry(labels)];
    draft.fields.labels.entries[0].key = 'same';
    draft.fields.labels.entries[1].key = 'same';
    const result = serializeStartInput(schema, draft);
    expect(result.json).toContain('"note":null');
    expect(result.errors).toContainEqual({ path: '$.labels', message: 'Map key "same" is duplicated.' });
  });

  it('reports nested array paths', () => {
    const arraySchema: FlowV2StartInputSchema = {
      kind: 'array', fixedLength: 1, items: { kind: 'integer', minimum: '0', maximum: '9' },
    };
    const draft = createStartInputDraft(arraySchema);
    draft.items[0].scalar = '10';
    expect(serializeStartInput(arraySchema, draft).errors[0].path).toBe('$[0]');
  });
});
