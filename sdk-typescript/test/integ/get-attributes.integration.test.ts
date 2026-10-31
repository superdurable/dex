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
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  Attribute,
  Client,
  FlowDefinitionError,
  FlowNotFoundError,
  InitialAttribute,
  openBlobCache,
  stringCodec,
} from "../../src/index.js";
import { expectError, flowId, withEnvironment } from "./environment.js";
import { getFlowAttributes } from "./flow-service-client.js";
import {
  GetAttributesFlow,
  getAttributesCount,
  getAttributesEmptyText,
  getAttributesLargeValue,
  getAttributesNote,
  getAttributesNullNote,
  getAttributesQuantities,
  getAttributesSecret,
  getAttributesStatus,
  getAttributesUnset,
  getAttributesZero,
} from "./get_attributes_flow.js";

const secret = "hard-delete-value".repeat(64);
const largeValue = "large-value".repeat(64);
const note = { text: "ship today", priority: 2 };

test("getAttributes reads typed committed values from running and closed runs", async () => {
  const flow = new GetAttributesFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("get-attributes");
    await client.startFlow(flow, id, undefined, {
      attributes: [
        InitialAttribute.of(getAttributesStatus, "accepted"),
        InitialAttribute.of(getAttributesEmptyText, ""),
        InitialAttribute.of(getAttributesCount, 7n),
        InitialAttribute.of(getAttributesZero, 0n),
        InitialAttribute.of(getAttributesNote, note),
        InitialAttribute.of(getAttributesNullNote, null),
        InitialAttribute.of(getAttributesSecret, secret),
        InitialAttribute.of(getAttributesLargeValue, largeValue),
        InitialAttribute.mapValue(getAttributesQuantities, "line-1", 0n),
      ],
    });

    const rawLargeValue = (await getFlowAttributes(id, [getAttributesLargeValue.name]))[0]?.value?.kind;
    assert.equal(rawLargeValue?.$case, "internalBlobIdForStringValue");

    const [
      status,
      emptyText,
      count,
      zero,
      unset,
      decodedNote,
      nullNote,
      decodedSecret,
      decodedLargeValue,
      lineOne,
      lineTwo,
    ] = await client.getAttributes(
      id,
      getAttributesStatus,
      getAttributesEmptyText,
      getAttributesCount,
      getAttributesZero,
      getAttributesUnset,
      getAttributesNote,
      getAttributesNullNote,
      getAttributesSecret,
      getAttributesLargeValue,
      getAttributesQuantities.instance("line-1"),
      getAttributesQuantities.instance("line-2"),
    );
    assert.equal(status, "accepted");
    assert.equal(emptyText, "");
    assert.equal(count, 7n);
    assert.equal(zero, 0n);
    assert.equal(unset, undefined);
    assert.deepEqual(decodedNote, note);
    assert.equal(nullNote, undefined, "a null write deletes the Attribute");
    assert.equal(decodedSecret, secret);
    assert.equal(decodedLargeValue, largeValue);
    assert.equal(lineOne, 0n);
    assert.equal(lineTwo, undefined);

    await assert.rejects(
      client.getAttributes(id, getAttributesCount, getAttributesCount),
      (error) => error instanceof TypeError
        && error.message.includes(id)
        && error.message.includes(`duplicate Attribute read ${getAttributesCount.name}`),
    );
    await assert.rejects(
      // @ts-expect-error getAttributes requires at least one read.
      client.getAttributes(id),
      (error) => error instanceof TypeError
        && error.message.includes(id)
        && error.message.includes("requires at least one Attribute read"),
    );

    const unregistered = new Attribute("get-attributes-unregistered", stringCodec);
    const unregisteredError = await expectError(client.getAttributes(id, unregistered), FlowDefinitionError);
    assert.ok(unregisteredError.message.includes(id));
    assert.match(unregisteredError.message, /get-attributes-unregistered is not registered/);
    const mismatchedCount = new Attribute(getAttributesCount.name, stringCodec);
    const mismatchedError = await expectError(client.getAttributes(id, mismatchedCount), FlowDefinitionError);
    assert.ok(mismatchedError.message.includes(id));
    assert.match(
      mismatchedError.message,
      /get-attributes-count differs from the definition registered by Flow GetAttributesFlow/,
    );

    await client.stopFlow(id);
    await waitUntilClosed(client, id);
    const cacheDirectory = mkdtempSync(join(tmpdir(), "dex-typescript-get-attributes-cache-"));
    const freshCache = openBlobCache({ directory: cacheDirectory, maxBytes: 64 * 1_024 * 1_024 });
    const freshClient = new Client(client.registry, freshCache, client.options);
    try {
      const [closedSecret, closedLargeValue, closedStatus] = await freshClient.getAttributes(
        id,
        getAttributesSecret,
        getAttributesLargeValue,
        getAttributesStatus,
      );
      assert.equal(closedSecret, secret);
      assert.equal(closedLargeValue, largeValue);
      assert.equal(closedStatus, "accepted");
    } finally {
      await freshClient.close();
      freshCache.close();
      rmSync(cacheDirectory, { recursive: true, force: true });
    }

    const missingId = flowId("get-attributes-missing");
    const missing = await expectError(client.getAttributes(missingId, getAttributesCount), FlowNotFoundError);
    assert.equal(missing.flowId, missingId);
    assert.equal(missing.operation, "getAttributes");
    assert.ok(missing.message.includes(missingId));
    assert.ok(missing.message.includes(getAttributesCount.name));
  });
});

async function waitUntilClosed(client: Client, id: string): Promise<void> {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    if ((await client.describeFlow(id)).status !== "running") {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`Flow ${id} did not close`);
}
