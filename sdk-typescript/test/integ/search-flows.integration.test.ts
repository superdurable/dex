// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";

import { stringCodec, type Client, type SearchFlowEntry } from "../../src/index.js";
import { flowId, withEnvironment } from "./environment.js";
import { KEYWORD_KEY, SearchFlowsFlow } from "./search_flows_flow.js";

test("searchFlows finds an indexed flow", async () => {
  const flow = new SearchFlowsFlow();
  await withEnvironment([flow], async ({ client }) => {
    const keywordValue = `sf-${randomUUID()}`;
    const id = flowId("search-flows");
    await client.startFlow(flow, id, keywordValue);
    assert.equal(await client.waitForFlow(id, stringCodec, 30_000), keywordValue);

    const query = `${KEYWORD_KEY} = '${keywordValue}'`;
    const entry = await pollForFlow(client, query, id);
    assert.equal(entry.flowId, id);
    assert.ok(entry.runId.length > 0);
    assert.equal(entry.status, "completed");
    assert.ok(entry.startedAt instanceof Date);
    assert.equal(entry.indexedAttributes.get(KEYWORD_KEY), keywordValue);
  });
});

test("searchFlows rejects a negative page size", async () => {
  const flow = new SearchFlowsFlow();
  await withEnvironment([flow], async ({ client }) => {
    await assert.rejects(client.searchFlows("CustomKeywordField = 'x'", -1), RangeError);
  });
});

async function pollForFlow(
  client: Client,
  query: string,
  id: string,
): Promise<SearchFlowEntry> {
  const deadline = Date.now() + 30_000;
  let lastError: unknown;
  while (Date.now() < deadline) {
    try {
      const page = await client.searchFlows(query, 100);
      const found = page.flows.find((entry) => entry.flowId === id);
      if (found !== undefined) {
        return found;
      }
    } catch (error) {
      lastError = error;
    }
    await delay(200);
  }
  throw new Error(`flow ${id} not found via searchFlows: ${String(lastError)}`);
}
