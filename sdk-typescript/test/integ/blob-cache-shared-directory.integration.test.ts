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
import { mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { format } from "node:util";

import { Client, InitialAttribute, Registry, Worker, openBlobCache, stringCodec } from "../../src/index.js";
import { flowId } from "./environment.js";
import { SharedBlobCacheFlow, sharedBlobCacheText } from "./shared_blob_cache_flow.js";

const serverAddress = process.env.DEX_SERVER_ADDRESS ?? "127.0.0.1:8801";
const cacheBytes = 64 * 1024 * 1024;
const largeText = "shared blob cache text ".repeat(512);

/** Records the console.warn lines that report a failed BlobCache write. */
function captureCacheWriteWarnings(): { readonly lines: string[]; restore(): void } {
  const lines: string[] = [];
  const original = console.warn;
  console.warn = (...items: unknown[]): void => {
    const line = format(...items);
    if (line.includes("to the BlobCache")) {
      lines.push(line);
      return;
    }
    original(...items);
  };
  return { lines, restore: () => { console.warn = original; } };
}

test("a Client, an RPC, and a Step hydrate a blob that another cache on the same directory stored", async () => {
  const flow = new SharedBlobCacheFlow();
  const registry = new Registry([flow]);
  const directory = mkdtempSync(join(tmpdir(), "dex-typescript-shared-blob-cache-"));
  // Two open caches on one directory, as two Worker processes started from one working directory have.
  const firstCache = openBlobCache({ directory, maxBytes: cacheBytes });
  const secondCache = openBlobCache({ directory, maxBytes: cacheBytes });
  const warnings = captureCacheWriteWarnings();
  const firstClient = new Client(registry, firstCache, { serverAddress });
  const secondClient = new Client(registry, secondCache, { serverAddress });
  const worker = new Worker(registry, secondCache, {
    serverAddress, pull: { appWorkerID: flowId("shared-blob-cache-worker") },
  });
  try {
    await worker.start();
    await worker.waitPullRegistered({ timeoutMs: 30_000 });
    const id = flowId("shared-blob-cache");
    await firstClient.startFlow(flow, id, undefined, {
      attributes: [InitialAttribute.of(sharedBlobCacheText, largeText)],
    });

    assert.deepEqual(await firstClient.getAttributes(id, sharedBlobCacheText), [largeText]);
    assert.equal(warnings.lines.length, 0, `the first cache stores the blob: ${warnings.lines.join("\n")}`);

    // The second cache keeps the identical file that the first cache wrote, so no write fails.
    assert.deepEqual(await secondClient.getAttributes(id, sharedBlobCacheText), [largeText]);
    assert.equal(warnings.lines.length, 0, `the second cache stores the same blob: ${warnings.lines.join("\n")}`);

    assert.equal(await secondClient.invokeRPC(flow.readTextLength, id), String(largeText.length));
    await secondClient.invokeRPC(flow.reportTextLength, id);
    const result = await secondClient.waitForFlow(id);
    assert.equal(result.singleOutput(stringCodec), String(largeText.length));
    // The Worker shares the second cache, so its RPC and Step read the kept blob and never write it.
    assert.equal(
      warnings.lines.length,
      0,
      `the Worker's RPC and Step read the kept blob without a failed write: ${warnings.lines.join("\n")}`,
    );
  } finally {
    warnings.restore();
    await secondClient.close();
    await firstClient.close();
    await worker.close();
    secondCache.close();
    firstCache.close();
    rmSync(directory, { recursive: true, force: true });
  }
});

test("a Client, an RPC, and a Step use the loaded blob when another cache stored different bytes for it", async () => {
  const flow = new SharedBlobCacheFlow();
  const registry = new Registry([flow]);
  const directory = mkdtempSync(join(tmpdir(), "dex-typescript-shared-blob-cache-"));
  const firstCache = openBlobCache({ directory, maxBytes: cacheBytes });
  const secondCache = openBlobCache({ directory, maxBytes: cacheBytes });
  const warnings = captureCacheWriteWarnings();
  const firstClient = new Client(registry, firstCache, { serverAddress });
  const secondClient = new Client(registry, secondCache, { serverAddress });
  const worker = new Worker(registry, secondCache, {
    serverAddress, pull: { appWorkerID: flowId("blob-mismatch-worker") },
  });
  try {
    await worker.start();
    await worker.waitPullRegistered({ timeoutMs: 30_000 });
    const id = flowId("shared-blob-cache-mismatch");
    await firstClient.startFlow(flow, id, undefined, {
      attributes: [InitialAttribute.of(sharedBlobCacheText, largeText)],
    });
    assert.deepEqual(await firstClient.getAttributes(id, sharedBlobCacheText), [largeText]);

    // Change one payload byte of the first cache's file and keep its length, so only a byte comparison tells.
    const blobFiles = readdirSync(join(directory, "blobs"), { recursive: true, encoding: "utf8" })
      .filter((name) => name.endsWith(".blob"))
      .map((name) => join(directory, "blobs", name));
    assert.equal(blobFiles.length, 1, blobFiles.join("\n"));
    const stored = readFileSync(blobFiles[0]!);
    stored[stored.length - 1] = stored[stored.length - 1]! ^ 0x01;
    writeFileSync(blobFiles[0]!, stored);

    // Each read loads the blob from Dex, fails to store it ("blob ID content mismatch"), warns, and uses the value.
    assert.deepEqual(await secondClient.getAttributes(id, sharedBlobCacheText), [largeText]);
    assert.equal(warnings.lines.length, 1, "the second Client cannot store the blob");
    assert.ok(warnings.lines[0]?.includes(`of Flow ${id} to the BlobCache`), warnings.lines[0]);
    assert.ok(warnings.lines[0]?.includes("blob ID content mismatch"), warnings.lines[0]);

    assert.equal(await secondClient.invokeRPC(flow.readTextLength, id), String(largeText.length));
    assert.ok(warnings.lines.length >= 2, "the Worker's RPC also fails to store the blob");

    const beforeStep = warnings.lines.length;
    await secondClient.invokeRPC(flow.reportTextLength, id);
    const result = await secondClient.waitForFlow(id);
    assert.equal(result.singleOutput(stringCodec), String(largeText.length));
    assert.ok(warnings.lines.length > beforeStep, "the Worker's Step also fails to store the blob");
  } finally {
    warnings.restore();
    await secondClient.close();
    await firstClient.close();
    await worker.close();
    secondCache.close();
    firstCache.close();
    rmSync(directory, { recursive: true, force: true });
  }
});
