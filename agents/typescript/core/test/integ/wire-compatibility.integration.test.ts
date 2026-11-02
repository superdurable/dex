// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import {
  decodeAgentInterrupt,
  decodeAgentInterruptResolution,
  decodeAgentToolCall,
  encodeAgentInterrupt,
  encodeAgentInterruptResolution,
  encodeAgentToolCall,
} from "../../src/agent-session-codecs.js";

// Wire values exactly as @superdurable/dex-agents 0.1.0 encodes them.
const recordOf010 = {
  tool_call_id: "call-1",
  turn_id: "turn-1",
  tool_name: "send_email",
  arguments: '{"to":"ops"}',
  effect: "unclassified",
  requires_approval: true,
  status: "awaiting_approval",
  output: null,
  error: null,
  rejection_message: null,
  deferral_order: 0,
};
const interruptOf010 = {
  interrupt_id: "call-1",
  kind: "tool_approval",
  tool_call_id: "call-1",
  tool_name: "send_email",
  arguments: '{"to":"ops"}',
  created_at: "2026-11-01T12:00:00.000Z",
  deadline: null,
};
const resolutionOf010 = { approved: false, rejection_message: "not today" };

describe("wire compatibility between 0.1.0 and 0.2.0", () => {
  test("values a 0.1.0 Worker wrote decode in 0.2.0 with the new fields at their defaults", () => {
    assert.deepEqual(decodeAgentToolCall(recordOf010), {
      toolCallId: "call-1",
      turnId: "turn-1",
      toolName: "send_email",
      arguments: '{"to":"ops"}',
      effect: "unclassified",
      requiresApproval: true,
      status: "awaiting_approval",
      output: null,
      error: null,
      rejectionMessage: null,
      deferralOrder: 0,
      waitUntil: null,
      inputContinuation: null,
      inputAnswers: [],
      inputQuestion: null,
      inputChoices: [],
      inputAllowsFreeText: true,
    });
    assert.deepEqual(decodeAgentInterrupt(interruptOf010), {
      interruptId: "call-1",
      kind: "tool_approval",
      toolCallId: "call-1",
      toolName: "send_email",
      arguments: '{"to":"ops"}',
      createdAt: "2026-11-01T12:00:00.000Z",
      deadline: null,
      question: null,
      choices: [],
      allowsFreeText: true,
    });
    assert.deepEqual(decodeAgentInterruptResolution(resolutionOf010), {
      approved: false,
      rejectionMessage: "not today",
      answer: null,
    });
  });

  test("values 0.2.0 writes with no new option enabled keep every 0.1.0 field, so a 0.1.0 Worker decodes them", () => {
    const pairs: [Readonly<Record<string, unknown>>, Readonly<Record<string, unknown>>][] = [
      [recordOf010, encodeAgentToolCall(decodeAgentToolCall(recordOf010)) as Record<string, unknown>],
      [interruptOf010, encodeAgentInterrupt(decodeAgentInterrupt(interruptOf010)) as Record<string, unknown>],
      [
        resolutionOf010,
        encodeAgentInterruptResolution(decodeAgentInterruptResolution(resolutionOf010)) as Record<string, unknown>,
      ],
    ];
    for (const [valueOf010, valueOf020] of pairs) {
      // A 0.1.0 decoder reads only its own fields, so the 0.2.0 value must repeat each one unchanged.
      for (const [field, value] of Object.entries(valueOf010)) {
        assert.deepEqual(valueOf020[field], value, `field ${field}`);
      }
    }
  });
});
