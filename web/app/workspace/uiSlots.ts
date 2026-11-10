// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import type { V2Flow } from '@/lib/types';

/**
 * Where a Flow says each of its Attributes belongs, read off the Display contract.
 *
 * Declared once, on Display, and resolved against whatever the reader actually has: a list row holds
 * indexed Attributes and a Summary read, a drawer holds the whole Display. So one declaration serves
 * both surfaces and each draws the slots it can reach.
 */
export type UISlotName = 'title' | 'subtitle' | 'status' | 'recommendation' | 'reason';

export interface UISlottedField {
  attributeKey: string;
  description: string;
}

/** Fields per slot, in declaration order. Only `reason` is ever longer than one. */
export type UISlotMap = Partial<Record<UISlotName, UISlottedField[]>>;

const UI_SLOT_NAMES: readonly UISlotName[] = ['title', 'subtitle', 'status', 'recommendation', 'reason'];

function isUISlotName(value: string): value is UISlotName {
  return (UI_SLOT_NAMES as readonly string[]).includes(value);
}

export function uiSlotsOf(definition: FlowV2Definition | undefined): UISlotMap {
  const map: UISlotMap = {};
  for (const field of definition?.display.fields ?? []) {
    const uiSlot = field.uiSlot;
    if (uiSlot === undefined || !isUISlotName(uiSlot)) continue;
    map[uiSlot] = [...(map[uiSlot] ?? []), {
      attributeKey: field.attributeKey,
      description: field.description,
    }];
  }
  return map;
}

/**
 * What a list row can say about a run.
 *
 * Every part has a fallback, because a slot is optional and most Flows declare none: a row must read
 * the same for a Flow that filled every slot and one that filled none.
 */
export interface RunRow {
  /** Names the run to a person. The Flow ID when nothing better is declared. */
  title: string;
  /** True when the title is the Flow ID, so the row can stop repeating it. */
  titleIsFlowID: boolean;
  /** Where the run has got to, in the Flow's own words when it has any. */
  status: string;
}

export function runRow(flow: V2Flow, uiSlots: UISlotMap): RunRow {
  const title = firstValue(flow, uiSlots.title);
  const status = firstValue(flow, uiSlots.status);
  return {
    title: title ?? flow.flowId,
    titleIsFlowID: title === null,
    status: status ?? flow.flowStatus,
  };
}

/**
 * The first slotted Attribute this run carries a value for.
 *
 * Indexed Attributes before Summary only because a row is more likely to be filtered on one; either
 * source is equally live. A slot whose Attribute is Display-only resolves to null here and is drawn
 * by the drawer instead.
 */
function firstValue(flow: V2Flow, fields: UISlottedField[] | undefined): string | null {
  for (const field of fields ?? []) {
    const value = flow.indexedAttributes[field.attributeKey] ?? flow.summary?.[field.attributeKey];
    if (value === null || value === undefined || value === '') continue;
    return String(value);
  }
  return null;
}

/**
 * The slotted Display fields, in slot order rather than declaration order.
 *
 * Slot order is the order a reader needs them: what this is, what it is about, where it has got to,
 * what is proposed, and why. Declaration order is whatever the author happened to type.
 */
export function leadFields(
  definition: FlowV2Definition,
): FlowV2Definition['display']['fields'] {
  const byUISlot = new Map<string, FlowV2Definition['display']['fields']>();
  for (const field of definition.display.fields) {
    if (field.uiSlot === undefined || !isUISlotName(field.uiSlot)) continue;
    byUISlot.set(field.uiSlot, [...(byUISlot.get(field.uiSlot) ?? []), field]);
  }
  return UI_SLOT_NAMES.flatMap((uiSlot) => byUISlot.get(uiSlot) ?? []);
}
