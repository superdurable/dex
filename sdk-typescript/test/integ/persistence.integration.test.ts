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
    const id = flowId("persistence");
    await client.startFlow(flow, id, "input", {
      attributes: [
        InitialAttribute.of(flow.initial, "initial"),
        InitialAttribute.mapValue(flow.dataMap, "one", "initial"),
      ],
    });
    assert.equal(await client.waitForFlow(id, 30_000).then((result) => result.singleOutput(stringCodec)), "input");
  });
});

test("RPC sets every indexed attribute kind", async () => {
  const flow = new SetAttributesFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("set-indexed-attributes");
    await client.startFlow(flow, id, "start");
    await client.invokeRPC(flow.setIndexed, id);
    await client.invokeRPC(flow.complete, id);

    assert.equal(await client.waitForFlow(id, 30_000).then((result) => result.singleOutput(stringCodec)), "test-result");
  });
});

test("RPC sets primitive, mapped, and model data attributes", async () => {
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
    await client.invokeRPC(flow.setData, id, "query-start");
    assert.equal(await waiting, "query-start");
    const waitingMap = client.waitForAttributeMatch(
      id,
      flow.dataMap,
      "special % key",
      AttributeMatch.equalTo("mapped-value"),
      30_000,
    );
    await client.invokeRPC(flow.setMapOne, id, "mapped-value");
    await client.invokeRPC(flow.setMapSpecial, id, "mapped-value");
    assert.equal(await waitingMap, "mapped-value");
    await client.invokeRPC(flow.setInteger, id, 3);
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
    await client.invokeRPC(flow.setModel, id, { value: 7 });
    await client.invokeRPC(flow.complete, id);

    assert.equal(await client.waitForFlow(id, 30_000).then((result) => result.singleOutput(stringCodec)), "test-result");
  });
});
