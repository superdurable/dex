// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import test from "node:test";

import { Server, ServerCredentials, status as grpcStatus, type sendUnaryData } from "@grpc/grpc-js";

import { Attribute, IndexType, Registry, StepList, Worker, stringCodec, type BlobCache, type Flow } from "../src/index.js";
import {
  FlowServiceService, IndexType as ProtoIndexType, WorkerTaskServiceService,
  type ServerInfo, type FlowServiceServer, type WorkerTaskServiceServer,
  type SyncAttributeIndexRequest, type SyncAttributeIndexResponse,
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

test("Worker synchronizes indexes before opening task streams", async () => {
  let received: SyncAttributeIndexRequest | undefined;
  const calls: string[] = [];
  let hasPolled = false;
  const flowServer = new Server();
  flowServer.addService(WorkerTaskServiceService, {
    pollWorkerTask(call) {
      hasPolled = true;
      call.on("end", () => call.end());
      call.on("data", () => call.write({ frame: { $case: "registered", value: { supportsUnknownNameRejection: true } } }));
    },
  } as WorkerTaskServiceServer);
  flowServer.addService(FlowServiceService, {
    getServerInfo(_call, callback: sendUnaryData<ServerInfo>) {
      calls.push("GetServerInfo");
      callback(null, compatibleServerInfo());
    },
    syncAttributeIndexes(call, callback: sendUnaryData<SyncAttributeIndexResponse>) {
      calls.push("SyncAttributeIndexes");
      received = call.request as SyncAttributeIndexRequest;
      assert.equal(hasPolled, false);
      callback(null, {});
    },
  } as Partial<FlowServiceServer> as FlowServiceServer);
  const flowPort = await bind(flowServer);
  const worker = new Worker(new Registry([new IndexedFlow()]), new MemoryBlobCache(), {
    serverAddress: `127.0.0.1:${flowPort}`,
  });
  try {
    await worker.start();
    assert.equal(
      received?.attributeIndexes.TypeScriptWorkerStatus,
      ProtoIndexType.INDEX_TYPE_KEYWORD,
    );
    assert.deepEqual(calls, ["GetServerInfo", "SyncAttributeIndexes"]);
    await worker.waitPullRegistered({ timeoutMs: 5_000 });
    assert.equal(hasPolled, true);
  } finally {
    await worker.close();
    await shutdown(flowServer);
  }
});

test("Worker rejects incompatible Server before sync and task streams", async () => {
  const calls: string[] = [];
  let hasPolled = false;
  const flowServer = new Server();
  flowServer.addService(WorkerTaskServiceService, {
    pollWorkerTask(call) {
      hasPolled = true;
      call.on("end", () => call.end());
      call.on("data", () => call.write({ frame: { $case: "registered", value: { supportsUnknownNameRejection: true } } }));
    },
  } as WorkerTaskServiceServer);
  flowServer.addService(FlowServiceService, {
    getServerInfo(_call, callback: sendUnaryData<ServerInfo>) {
      calls.push("GetServerInfo");
      callback(null, {
        serverVersion: "future",
        minimumSupportedProtocolVersion: 4,
        currentProtocolVersion: 4,
        serverEngine: 0,
      });
    },
    syncAttributeIndexes(_call, callback: sendUnaryData<SyncAttributeIndexResponse>) {
      calls.push("SyncAttributeIndexes");
      callback(null, {});
    },
  } as Partial<FlowServiceServer> as FlowServiceServer);
  const flowPort = await bind(flowServer);
  const worker = new Worker(new Registry([new IndexedFlow()]), new MemoryBlobCache(), {
    serverAddress: `127.0.0.1:${flowPort}`,
  });
  try {
    await assert.rejects(worker.start(), (failure: Error) => {
      assert.match(String(failure.cause), /protocol intervals do not overlap/);
      return true;
    });
    assert.deepEqual(calls, ["GetServerInfo"]);
    assert.equal(hasPolled, false);
  } finally {
    await worker.close();
    await shutdown(flowServer);
  }
});

test("Worker sync failure prevents task streams", async () => {
  let hasPolled = false;
  const flowServer = new Server();
  flowServer.addService(WorkerTaskServiceService, {
    pollWorkerTask(call) {
      hasPolled = true;
      call.on("end", () => call.end());
      call.on("data", () => call.write({ frame: { $case: "registered", value: { supportsUnknownNameRejection: true } } }));
    },
  } as WorkerTaskServiceServer);
  flowServer.addService(FlowServiceService, {
    getServerInfo(_call, callback: sendUnaryData<ServerInfo>) {
      callback(null, compatibleServerInfo());
    },
    syncAttributeIndexes(_call, callback: sendUnaryData<SyncAttributeIndexResponse>) {
      callback({ code: grpcStatus.PERMISSION_DENIED, details: "denied", name: "Error", message: "denied" });
    },
  } as Partial<FlowServiceServer> as FlowServiceServer);
  const flowPort = await bind(flowServer);
  const worker = new Worker(new Registry([new IndexedFlow()]), new MemoryBlobCache(), {
    serverAddress: `127.0.0.1:${flowPort}`,
  });
  try {
    await assert.rejects(worker.start(), /cannot start TypeScript pull Worker/);
    assert.equal(hasPolled, false);
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

function compatibleServerInfo(): ServerInfo {
  return {
    serverVersion: "test",
    minimumSupportedProtocolVersion: 1,
    currentProtocolVersion: 1,
    serverEngine: 0,
  };
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
