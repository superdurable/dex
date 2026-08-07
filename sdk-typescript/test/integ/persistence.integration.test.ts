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
import test from "node:test";

import { InitialAttribute, stringCodec } from "../../src/index.js";
import { BasicPersistenceFlow } from "./basic_persistence_flow.js";
import { flowId, withEnvironment } from "./environment.js";
import { SetAttributesFlow } from "./set_attributes_flow.js";

test("persistence reads initial values, Step writes, locals, and deletes", async () => {
  const flow = new BasicPersistenceFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("persistence");
    await client.startFlow(flow, id, "input", {
      attributes: [
        InitialAttribute.of(flow.initial, "initial"),
        InitialAttribute.mapValue(flow.dataMap, "one", "initial"),
      ],
    });
    assert.equal(await client.waitForFlow(id, stringCodec, 30_000), "input");
    assert.equal(await client.getAttribute(id, flow.data), "input");
    assert.equal(await client.getAttribute(id, flow.initial), "initial");
    assert.equal(await client.getAttribute(id, flow.dataMap, "one"), undefined);
    assert.equal(await client.getAttribute(id, flow.keyword), "input");
    assert.equal(await client.getAttribute(id, flow.integer), 1);
    assert.equal(
      (await client.getAttribute(id, flow.datetime))?.toISOString(),
      "2023-04-17T21:17:49.000Z",
    );
    assert.equal((await client.getAttribute(id, flow.model))?.value, 0);
  });
});

test("Client sets every indexed attribute kind", async () => {
  const flow = new SetAttributesFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("set-search-attributes");
    const keywords = ["keyword-1", "keyword-2"] as const;
    const datetime = new Date("2024-11-13T00:00:01.731Z");
    await client.startFlow(flow, id, "start");
    await client.setAttribute(id, flow.keyword, "keyword-1");
    await client.setAttribute(id, flow.text, "text-1");
    await client.setAttribute(id, flow.decimal, 1);
    await client.setAttribute(id, flow.integer, 1);
    await client.setAttribute(id, flow.bool, true);
    await client.setAttribute(id, flow.keywords, keywords);
    await client.setAttribute(id, flow.datetime, datetime);
    await client.publish(id, flow.proceed, undefined);

    assert.equal(await client.waitForFlow(id, stringCodec, 30_000), "test-result");
    assert.equal(await client.getAttribute(id, flow.keyword), "keyword-1");
    assert.equal(await client.getAttribute(id, flow.text), "text-1");
    assert.equal(await client.getAttribute(id, flow.decimal), 1);
    assert.equal(await client.getAttribute(id, flow.integer), 1);
    assert.equal(await client.getAttribute(id, flow.bool), true);
    assert.deepEqual(await client.getAttribute(id, flow.keywords), keywords);
    assert.equal((await client.getAttribute(id, flow.datetime))?.toISOString(), datetime.toISOString());
  });
});

test("Client sets scalar, mapped, and model data attributes", async () => {
  const flow = new SetAttributesFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("set-data-attributes");
    await client.startFlow(flow, id, "start");
    await client.setAttribute(id, flow.data, "query-start");
    await client.setAttribute(id, flow.dataMap, "one", "mapped-value");
    await client.setAttribute(id, flow.model, { value: 7 });
    await client.publish(id, flow.proceed, undefined);

    assert.equal(await client.waitForFlow(id, stringCodec, 30_000), "test-result");
    assert.equal(await client.getAttribute(id, flow.data), "query-start");
    assert.equal(await client.getAttribute(id, flow.dataMap, "one"), "mapped-value");
    assert.equal((await client.getAttribute(id, flow.model))?.value, 7);
  });
});
