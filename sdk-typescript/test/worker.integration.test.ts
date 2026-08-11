// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import assert from "node:assert/strict";
import { createConnection, createServer } from "node:net";
import test from "node:test";

import {
  Server,
  ServerCredentials,
  status as grpcStatus,
  type sendUnaryData,
} from "@grpc/grpc-js";

import {
  Attribute,
  IndexType,
  Registry,
  StepList,
  Worker,
  stringCodec,
  type BlobCache,
  type Flow,
} from "../src/index.js";
import {
  FlowServiceService,
  IndexType as ProtoIndexType,
  type FlowServiceServer,
  type SyncAttributeIndexRequest,
  type SyncAttributeIndexResponse,
} from "../src/gen/dex.js";

class IndexedFlow implements Flow {
  private readonly status = new Attribute(
    "status",
    stringCodec,
    { type: IndexType.KEYWORD, indexKey: "TypeScriptWorkerStatus" },
  );

  public getFlowType(): string {
    return "IndexedFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.empty();
  }

  public getPersistenceSchema() {
    return { attributes: [this.status] };
  }
}

test("Worker synchronizes indexes before listening", async () => {
  const workerPort = await availablePort();
  let received: SyncAttributeIndexRequest | undefined;
  let listeningDuringSync: boolean | undefined;
  const flowServer = new Server();
  flowServer.addService(FlowServiceService, {
    syncAttributeIndexes(call, callback: sendUnaryData<SyncAttributeIndexResponse>) {
      received = call.request as SyncAttributeIndexRequest;
      void canConnect(workerPort).then((listening) => {
        listeningDuringSync = listening;
        callback(null, {});
      }, callback);
    },
  } as Partial<FlowServiceServer> as FlowServiceServer);
  const flowPort = await bind(flowServer);
  const worker = new Worker(new Registry([new IndexedFlow()]), new MemoryBlobCache(), {
    bindAddress: `127.0.0.1:${workerPort}`,
    serverAddress: `127.0.0.1:${flowPort}`,
  });
  try {
    await worker.start();
    assert.equal(
      received?.attributeIndexes.TypeScriptWorkerStatus,
      ProtoIndexType.INDEX_TYPE_KEYWORD,
    );
    assert.equal(listeningDuringSync, false);
    assert.equal(await canConnect(workerPort), true);
  } finally {
    await worker.close();
    await shutdown(flowServer);
  }
});

test("Worker sync failure keeps its port closed", async () => {
  const workerPort = await availablePort();
  const flowServer = new Server();
  flowServer.addService(FlowServiceService, {
    syncAttributeIndexes(_call, callback: sendUnaryData<SyncAttributeIndexResponse>) {
      callback({ code: grpcStatus.PERMISSION_DENIED, details: "denied", name: "Error", message: "denied" });
    },
  } as Partial<FlowServiceServer> as FlowServiceServer);
  const flowPort = await bind(flowServer);
  const worker = new Worker(new Registry([new IndexedFlow()]), new MemoryBlobCache(), {
    bindAddress: `127.0.0.1:${workerPort}`,
    serverAddress: `127.0.0.1:${flowPort}`,
  });
  try {
    await assert.rejects(worker.start(), /cannot start TypeScript Worker/);
    assert.equal(await canConnect(workerPort), false);
  } finally {
    await worker.close();
    await shutdown(flowServer);
  }
});

class MemoryBlobCache implements BlobCache {
  public readonly config = { directory: "memory", maxBytes: 1_024 };

  public get(_blobId: string): Uint8Array | undefined {
    return undefined;
  }

  public put(_blobId: string, _payload: Uint8Array): boolean {
    return true;
  }

  public delete(_blobId: string): void {}

  public deleteAll(): void {}

  public close(): void {}
}

function availablePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (address === null || typeof address === "string") {
        server.close();
        reject(new Error("failed to allocate a port"));
        return;
      }
      server.close((error) => error === undefined ? resolve(address.port) : reject(error));
    });
  });
}

function canConnect(port: number): Promise<boolean> {
  return new Promise((resolve) => {
    const socket = createConnection({ host: "127.0.0.1", port });
    socket.once("connect", () => {
      socket.destroy();
      resolve(true);
    });
    socket.once("error", () => {
      socket.destroy();
      resolve(false);
    });
  });
}

function bind(server: Server): Promise<number> {
  return new Promise((resolve, reject) => {
    server.bindAsync("127.0.0.1:0", ServerCredentials.createInsecure(), (error, port) => {
      if (error !== null) {
        reject(error);
        return;
      }
      resolve(port);
    });
  });
}

function shutdown(server: Server): Promise<void> {
  return new Promise((resolve, reject) => {
    server.tryShutdown((error) => error === undefined ? resolve() : reject(error));
  });
}
