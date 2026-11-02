// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { after, before, describe, test } from "node:test";

import { SpanKind, type Attributes } from "@opentelemetry/api";
import {
  BasicTracerProvider,
  InMemorySpanExporter,
  SimpleSpanProcessor,
  type ReadableSpan,
} from "@opentelemetry/sdk-trace-base";
import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { readGenAIProviderNameOfClaudeProvider } from "../../src/claude-agent-runner.js";
import { ClaudeAgentTestHarness } from "../fixtures/claude-agent-test-harness.js";
import { fakeClaudeRequestModel, fakeClaudeResponseModel } from "../fixtures/fake-claude-cli-environment.js";

// Every ModelUsage.provider value the SDK types list; a gateway relays to an unnamed backend.
const claudeProviderCases: readonly (readonly [string, string | undefined])[] = [
  ["firstParty", "anthropic"],
  ["bedrock", "aws.bedrock"],
  ["mantle", "aws.bedrock"],
  ["vertex", "gcp.vertex_ai"],
  ["foundry", "foundry"],
  ["anthropicAws", "anthropicAws"],
  ["anthropicGoogleCloud", "anthropicGoogleCloud"],
  ["gateway", undefined],
  ["", undefined],
];
const modelAttributeNames = [
  "gen_ai.request.model",
  "gen_ai.response.model",
  "gen_ai.provider.name",
  "gen_ai.response.id",
  "gen_ai.response.finish_reasons",
];

describe("Claude agent chat spans", () => {
  let engine: PrivateDexEngine;
  let harness: ClaudeAgentTestHarness | undefined;
  const exporter = new InMemorySpanExporter();
  const tracerProvider = new BasicTracerProvider({ spanProcessors: [new SimpleSpanProcessor(exporter)] });

  before(async () => {
    engine = await PrivateDexEngine.start();
  });

  after(async () => {
    await harness?.close();
    await tracerProvider.shutdown();
    await engine?.stop();
  });

  test("chat spans name the CLI's model and drop per-response fields for several model calls", async () => {
    harness = await ClaudeAgentTestHarness.start(engine, true, undefined, { tracing: { tracerProvider } });
    const sessionId = harness.newSessionId("traced");
    await harness.sessions.startSession(sessionId, "say hi");
    await harness.waitForTurns(sessionId, 1);
    assert.equal(await harness.sessions.sendUserMessage(sessionId, 'call lookup {"query":"docs"}'), true);
    await harness.waitForTurns(sessionId, 2);
    assert.equal(await harness.sessions.sendUserMessage(sessionId, 'call charge {"amount":5}'), true);
    const snapshot = await harness.waitForTurns(sessionId, 3);
    assert.match(snapshot.messages.at(-1)?.text ?? "", /^charge: charged 5/);

    const chatSpans = exporter
      .getFinishedSpans()
      .filter((span) => span.name.startsWith("chat") && span.attributes["gen_ai.conversation.id"] === sessionId)
      .sort(compareStartTimes);
    assert.deepEqual(
      chatSpans.map((span) => [span.name, span.kind, span.attributes["dex.agent.turn.id"]]),
      [
        [`chat ${fakeClaudeRequestModel}`, SpanKind.CLIENT, "turn-1"],
        [`chat ${fakeClaudeRequestModel}`, SpanKind.INTERNAL, "turn-2"],
        [`chat ${fakeClaudeRequestModel}`, SpanKind.CLIENT, "turn-3"],
        [`chat ${fakeClaudeRequestModel}`, SpanKind.CLIENT, "turn-3"],
      ],
    );
    for (const span of chatSpans) {
      assert.equal(span.attributes["gen_ai.request.model"], fakeClaudeRequestModel);
      assert.equal(span.attributes["gen_ai.response.model"], fakeClaudeResponseModel);
      assert.equal(span.attributes["gen_ai.provider.name"], "anthropic");
    }
    const [answer, readToolTurn, deferral, resumedAnswer] = chatSpans.map((span) => span.attributes) as [
      Attributes,
      Attributes,
      Attributes,
      Attributes,
    ];
    assert.deepEqual(answer["gen_ai.response.finish_reasons"], ["end_turn"]);
    assert.deepEqual(deferral["gen_ai.response.finish_reasons"], ["tool_use"]);
    assert.deepEqual(resumedAnswer["gen_ai.response.finish_reasons"], ["end_turn"]);
    const responseIds = [answer, deferral, resumedAnswer].map((attributes) => String(attributes["gen_ai.response.id"]));
    assert.ok(responseIds.every((responseId) => responseId.startsWith("msg_")), responseIds.join(", "));
    assert.equal(new Set(responseIds).size, 3);
    assert.equal(answer["gen_ai.usage.output_tokens"], 5);
    // Two model calls: no single response ID, finish reason, or usage describes the segment.
    assert.equal(readToolTurn["gen_ai.response.id"], undefined);
    assert.equal(readToolTurn["gen_ai.response.finish_reasons"], undefined);
    assert.deepEqual(
      Object.keys(readToolTurn).filter((name) => name.startsWith("gen_ai.usage.")),
      [],
    );
  });
});

describe("Claude agent chat spans with a subagent", () => {
  let engine: PrivateDexEngine;
  let harness: ClaudeAgentTestHarness | undefined;
  const exporter = new InMemorySpanExporter();
  const tracerProvider = new BasicTracerProvider({ spanProcessors: [new SimpleSpanProcessor(exporter)] });

  before(async () => {
    engine = await PrivateDexEngine.start();
  });

  after(async () => {
    await harness?.close();
    await tracerProvider.shutdown();
    await engine?.stop();
  });

  test("a segment with a subagent model call is an INTERNAL chat span that names no model", async () => {
    harness = await ClaudeAgentTestHarness.start(engine, true, undefined, { tracing: { tracerProvider } });
    const sessionId = harness.newSessionId("subagent");
    // The subagent runs inline, then charge defers: one main call and one subagent call.
    await harness.sessions.startSession(sessionId, 'call Agent {"prompt":"the docs"} && call charge {"amount":5}');
    const snapshot = await harness.waitForTurns(sessionId, 1);
    assert.match(snapshot.messages.at(-1)?.text ?? "", /charge: charged 5/);
    assert.equal(snapshot.usage.modelCalls, 3);

    const chatSpans = exporter
      .getFinishedSpans()
      .filter((span) => span.name.startsWith("chat") && span.attributes["gen_ai.conversation.id"] === sessionId)
      .sort(compareStartTimes);
    assert.deepEqual(
      chatSpans.map((span) => [span.name, span.kind]),
      [
        ["chat", SpanKind.INTERNAL],
        [`chat ${fakeClaudeRequestModel}`, SpanKind.CLIENT],
      ],
    );
    assert.deepEqual(
      Object.keys(chatSpans[0]?.attributes ?? {}).filter(
        (name) => name.startsWith("gen_ai.usage.") || modelAttributeNames.includes(name),
      ),
      [],
    );
  });
});

describe("Claude provider names", () => {
  test("map to well-known gen_ai.provider.name values, pass others through, and drop a gateway", () => {
    assert.deepEqual(
      claudeProviderCases.map(([claudeProvider]) => [
        claudeProvider,
        readGenAIProviderNameOfClaudeProvider(claudeProvider),
      ]),
      claudeProviderCases,
    );
  });
});

function compareStartTimes(left: ReadableSpan, right: ReadableSpan): number {
  return left.startTime[0] - right.startTime[0] || left.startTime[1] - right.startTime[1];
}
