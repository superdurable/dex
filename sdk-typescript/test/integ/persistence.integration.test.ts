// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import assert from "node:assert/strict";
import test from "node:test";

import {
  Attribute,
  AttributeMatch,
  FlowNotActiveError,
  FlowNotFoundError,
  InitialAttribute,
  LongPollTimeoutError,
  bytesCodec,
  stringCodec,
  voidCodec,
} from "../../src/index.js";
import { BasicPersistenceFlow } from "./basic_persistence_flow.js";
import { expectError, flowId, withEnvironment } from "./environment.js";
import { SetAttributesFlow } from "./set_attributes_flow.js";

test("persistence reads initial values, Step writes, locals, and deletes", async () => {
  const flow = new BasicPersistenceFlow();
  await withEnvironment([flow], async ({ client }) => {
    await expectError(client.getAttribute(flowId("missing"), flow.data), FlowNotFoundError);
    const id = flowId("persistence");
    await client.startFlow(flow, id, "input", {
      attributes: [
        InitialAttribute.of(flow.initial, "initial"),
        InitialAttribute.mapValue(flow.dataMap, "one", "initial"),
      ],
    });
    assert.equal(await client.waitForFlow(id, 30_000).then((result) => result.singleOutput(stringCodec)), "input");
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
    await expectError(client.setAttribute(id, flow.data, "closed"), FlowNotActiveError);
  });
});

test("Client sets every indexed attribute kind", async () => {
  const flow = new SetAttributesFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("set-indexed-attributes");
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

    assert.equal(await client.waitForFlow(id, 30_000).then((result) => result.singleOutput(stringCodec)), "test-result");
    assert.equal(await client.getAttribute(id, flow.keyword), "keyword-1");
    assert.equal(await client.getAttribute(id, flow.text), "text-1");
    assert.equal(await client.getAttribute(id, flow.decimal), 1);
    assert.equal(await client.getAttribute(id, flow.integer), 1);
    assert.equal(await client.getAttribute(id, flow.bool), true);
    assert.deepEqual(await client.getAttribute(id, flow.keywords), keywords);
    assert.equal((await client.getAttribute(id, flow.datetime))?.toISOString(), datetime.toISOString());
  });
});

test("Client sets primitive, mapped, and model data attributes", async () => {
  const flow = new SetAttributesFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("set-data-attributes");
    await client.startFlow(flow, id, "start");
    await expectError(
      client.waitForAttributeMatch(id, flow.data, AttributeMatch.equalTo("never"), 1_000),
      LongPollTimeoutError,
    );
    const waiting = client.waitForAttributeMatch(
      id,
      flow.data,
      AttributeMatch.equalTo("query-start"),
      30_000,
    );
    await client.setAttribute(id, flow.data, "query-start");
    assert.equal(await waiting, "query-start");
    const waitingMap = client.waitForAttributeMatch(
      id,
      flow.dataMap,
      "special % key",
      AttributeMatch.equalTo("mapped-value"),
      30_000,
    );
    await client.setAttribute(id, flow.dataMap, "one", "mapped-value");
    await client.setAttribute(id, flow.dataMap, "special % key", "mapped-value");
    assert.equal(await waitingMap, "mapped-value");
    await client.setAttribute(id, flow.integer, 3);
    assert.equal(
      await client.waitForAttributeMatch(
        id,
        flow.integer,
        AttributeMatch.greaterThan(0),
        30_000,
      ),
      3,
    );
    await assert.rejects(
      client.waitForAttributeMatch(id, flow.model, AttributeMatch.equalTo({ value: 8 }), 30_000),
      /supports only string, boolean, integer, or number operands/,
    );
    await assert.rejects(
      client.waitForAttributeMatch(
        id,
        new Attribute("bytes", bytesCodec),
        AttributeMatch.equalTo(new Uint8Array([1])),
        30_000,
      ),
      /supports only string, boolean, integer, or number operands/,
    );
    await assert.rejects(
      client.waitForAttributeMatch(
        id,
        new Attribute("null", voidCodec),
        AttributeMatch.equalTo(undefined),
        30_000,
      ),
      /supports only string, boolean, integer, or number operands/,
    );
    await client.setAttribute(id, flow.model, { value: 7 });
    await client.publish(id, flow.proceed, undefined);

    assert.equal(await client.waitForFlow(id, 30_000).then((result) => result.singleOutput(stringCodec)), "test-result");
    assert.equal(await client.getAttribute(id, flow.data), "query-start");
    assert.equal(await client.getAttribute(id, flow.dataMap, "one"), "mapped-value");
    assert.equal((await client.getAttribute(id, flow.model))?.value, 7);
  });
});
