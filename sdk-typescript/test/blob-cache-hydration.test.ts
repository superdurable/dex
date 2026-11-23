// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import test from "node:test";
import { format } from "node:util";

import type { BlobCache } from "../src/index.js";
import {
  Value as ProtoValue,
  type FlowServiceClient,
  type LoadBlobsRequest,
  type LoadBlobsResponse,
} from "../src/gen/dex.js";
import { ValueHydrator } from "../src/value-mapper.js";

const flowId = "order-42";
const blobId = "zd1|run-1|14-0";
const blobReference = ProtoValue.create({ kind: { $case: "internalBlobIdForStringValue", value: blobId } });
const loadedValue = ProtoValue.create({ kind: { $case: "stringValue", value: "large order note" } });

/** Answers LoadBlobs with one loaded string Value for every requested blob. */
class LoadBlobsService {
  public loadBlobsCalls = 0;

  public loadBlobs(request: LoadBlobsRequest, callback: (error: null, response: LoadBlobsResponse) => void): void {
    this.loadBlobsCalls += 1;
    const values = Object.fromEntries(request.entries.map((entry) => {
      assert.equal(entry.flowId, flowId);
      assert.equal(entry.blobValue?.kind?.$case, "internalBlobIdForStringValue");
      return [entry.blobValue.kind.value, loadedValue];
    }));
    callback(null, { values });
  }
}

function hydratorFor(service: LoadBlobsService, cache: BlobCache): ValueHydrator {
  return new ValueHydrator(service as unknown as FlowServiceClient, cache);
}

/** Runs `operation` while recording each console.warn call as the text Node would print. */
async function captureWarnings<T>(operation: () => Promise<T>): Promise<{ result: T; warnings: string[] }> {
  const warnings: string[] = [];
  const original = console.warn;
  console.warn = (...items: unknown[]): void => {
    warnings.push(format(...items));
  };
  try {
    return { result: await operation(), warnings };
  } finally {
    console.warn = original;
  }
}

function assertOneCacheWriteWarning(warnings: readonly string[], cause: RegExp): void {
  assert.equal(warnings.length, 1, warnings.join("\n"));
  const warning = warnings[0] ?? "";
  assert.ok(warning.startsWith(`cannot write blob ${blobId} of Flow ${flowId} to the BlobCache: `), warning);
  assert.ok(warning.endsWith("; hydration continues with the value loaded from Dex"), warning);
  assert.ok(!warning.includes("\n"), `the warning is one line: ${warning}`);
  assert.match(warning, cause);
}

test("a BlobCache whose put throws only logs a warning, and hydration returns the loaded value", async () => {
  const cacheFailure = new Error("disk full");
  const cacheWithFailingPut = {
    config: { directory: "/unused", maxBytes: 1 },
    get: () => undefined,
    put: () => {
      throw cacheFailure;
    },
    delete: () => undefined,
    deleteAll: () => undefined,
    close: () => undefined,
  } satisfies BlobCache;
  const service = new LoadBlobsService();
  const hydrator = hydratorFor(service, cacheWithFailingPut);

  const single = await captureWarnings(() => hydrator.hydrate(flowId, blobReference));
  assert.deepEqual(single.result, loadedValue);
  assertOneCacheWriteWarning(single.warnings, /Error: disk full/);

  const batch = await captureWarnings(() => hydrator.hydrateAll(flowId, [blobReference, blobReference]));
  assert.deepEqual(batch.result, [loadedValue, loadedValue]);
  assertOneCacheWriteWarning(batch.warnings, /Error: disk full/);
  assert.equal(service.loadBlobsCalls, 2, "nothing was cached, so each call loads the blob once");
});

test("a put error that cannot be converted to text is logged without it, and hydration still succeeds", async () => {
  // String() throws for an object without toString or valueOf.
  const unformattableFailure: unknown = Object.create(null);
  const cacheWithFailingPut = {
    config: { directory: "/unused", maxBytes: 1 },
    get: () => undefined,
    put: () => {
      throw unformattableFailure;
    },
    delete: () => undefined,
    deleteAll: () => undefined,
    close: () => undefined,
  } satisfies BlobCache;
  const { result, warnings } = await captureWarnings(() =>
    hydratorFor(new LoadBlobsService(), cacheWithFailingPut).hydrateAll(flowId, [blobReference]));
  assert.deepEqual(result, [loadedValue]);
  assert.deepEqual(warnings, [
    `cannot write blob ${blobId} of Flow ${flowId} to the BlobCache; hydration continues with the value loaded from Dex`,
  ]);
});

test("a failed BlobCache read and a blob Dex did not hydrate still fail the hydration", async () => {
  const cacheWithFailingGet = {
    config: { directory: "/unused", maxBytes: 1 },
    get: () => {
      throw new Error("cannot read");
    },
    put: () => true,
    delete: () => undefined,
    deleteAll: () => undefined,
    close: () => undefined,
  } satisfies BlobCache;
  await assert.rejects(
    hydratorFor(new LoadBlobsService(), cacheWithFailingGet).hydrate(flowId, blobReference),
    /the BlobCache failed/,
  );

  const omittingService = {
    loadBlobs(_request: LoadBlobsRequest, callback: (error: null, response: LoadBlobsResponse) => void): void {
      callback(null, { values: {} });
    },
  } as unknown as FlowServiceClient;
  const emptyCache = { ...cacheWithFailingGet, get: () => undefined } satisfies BlobCache;
  await assert.rejects(new ValueHydrator(omittingService, emptyCache).hydrate(flowId, blobReference), /did not hydrate blob/);
  await assert.rejects(new ValueHydrator(omittingService, emptyCache).hydrateAll(flowId, [blobReference]), /did not hydrate blob/);
});
