// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  BlobMessageTooLargeError,
  Client,
  InitialAttribute,
  Registry,
  Worker,
  openBlobCache,
  stringCodec,
  type BlobCache,
  type Flow,
} from "../../src/index.js";
import { expectError, flowId } from "./environment.js";
import { GrpcMessageSizeFlow, LargeRpcFlow, fiveMebibytes, grpcSizeValues } from "./grpc_message_size_flow.js";

const serverAddress = process.env.DEX_SERVER_ADDRESS ?? "127.0.0.1:8801";
const twoMebibytes = 2 * 1024 * 1024;

function withFreshBlobCache<Result>(run: (cache: BlobCache) => Promise<Result>): Promise<Result> {
  const directory = mkdtempSync(join(tmpdir(), "dex-typescript-message-size-"));
  const cache = openBlobCache({ directory, maxBytes: 128 * 1024 * 1024 });
  return run(cache).finally(() => {
    cache.close();
    rmSync(directory, { recursive: true, force: true });
  });
}

async function withClient<Result>(
  flow: Flow<any>,
  grpcMaxMessageBytes: number,
  run: (client: Client) => Promise<Result>,
): Promise<Result> {
  return withFreshBlobCache(async (cache) => {
    const client = new Client(new Registry([flow]), cache, { serverAddress, grpcMaxMessageBytes });
    try {
      return await run(client);
    } finally {
      await client.close();
    }
  });
}

async function withWorker<Result>(
  flow: Flow<any>,
  grpcMaxMessageBytes: number,
  run: (worker: Worker) => Promise<Result>,
): Promise<Result> {
  return withFreshBlobCache(async (cache) => {
    const worker = new Worker(new Registry([flow]), cache, {
      serverAddress, grpcMaxMessageBytes, pull: { appWorkerID: flowId("grpc-size-worker") },
    });
    await worker.start();
    try {
      await worker.waitPullRegistered({ timeoutMs: 30_000 });
      return await run(worker);
    } finally {
      await worker.close();
    }
  });
}

function countLoadBlobsCalls(worker: Worker): { count: number } {
  const service = Reflect.get(worker, "flowService") as { loadBlobs: (...parameters: unknown[]) => unknown };
  const loadBlobs = service.loadBlobs.bind(service);
  const calls = { count: 0 };
  service.loadBlobs = (...parameters: unknown[]) => {
    calls.count += 1;
    return loadBlobs(...parameters);
  };
  return calls;
}

function initialValues(valueBytes: number) {
  return grpcSizeValues.map((value, index) => InitialAttribute.of(value, String(index).repeat(valueBytes)));
}

test("a default Client receives a response over 4 MiB", async () => {
  const flow = new LargeRpcFlow();
  await withWorker(flow, 0, () => withClient(flow, 0, async (client) => {
    const id = flowId("grpc-size-rpc");
    await client.startFlow(flow, id, undefined);
    const output = await client.invokeRPC(flow.readLargeText, id);
    assert.equal(output.length, fiveMebibytes);
  }));
});

test("a default Worker receives a task over 4 MiB", async () => {
  const flow = new LargeRpcFlow();
  await withWorker(flow, 0, () => withClient(flow, 0, async (client) => {
    const id = flowId("grpc-size-task");
    await client.startFlow(flow, id, undefined);
    assert.equal(await client.invokeRPC(flow.measureText, id, "t".repeat(fiveMebibytes)), String(fiveMebibytes));
  }));
});

test("a Worker with an empty blob cache loads values over its limit through several calls", async () => {
  const flow = new GrpcMessageSizeFlow();
  await withWorker(flow, 3 * 1024 * 1024, (worker) => withClient(flow, 0, async (client) => {
    const calls = countLoadBlobsCalls(worker);
    const id = flowId("grpc-size-worker");
    await client.startFlow(flow, id, undefined, { attributes: initialValues(twoMebibytes) });
    const result = await client.waitForFlow(id);
    assert.equal(result.singleOutput(stringCodec), String(4 * twoMebibytes));
    assert.ok(calls.count >= 4, `expected several LoadBlobs calls, got ${calls.count}`);
  }));
});

test("a Client limit below one blob rejects with the named error", async () => {
  const flow = new GrpcMessageSizeFlow();
  const id = flowId("grpc-size-too-large");
  await withWorker(flow, 0, () => withClient(flow, 0, async (client) => {
    await client.startFlow(flow, id, undefined, { attributes: initialValues(twoMebibytes) });
    await client.waitForFlow(id);
  }));
  await withClient(flow, 1024 * 1024, async (client) => {
    const failure = await expectError(client.getAttributes(id, grpcSizeValues[0]!), BlobMessageTooLargeError);
    assert.equal(failure.flowId, id);
    assert.notEqual(failure.blobId, "");
    assert.ok(failure.message.includes(failure.blobId));
    assert.ok(failure.message.includes(id));
    assert.match(failure.message, /1048576/);
  });
});
