// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import test from "node:test";

import { Server, ServerCredentials, status as grpcStatus, type sendUnaryData, type ServiceError } from "@grpc/grpc-js";

import {
  Attribute, IndexType, PullRegistrationError, Registry, StepList, Worker, stringCodec, type BlobCache, type Flow,
} from "../src/index.js";
import {
  FlowServiceService, IndexType as ProtoIndexType, WorkerTaskServiceService,
  type PollWorkerTaskRequest, type ServerInfo, type FlowServiceServer, type WorkerTaskServiceServer,
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
      call.on("data", () => call.write({ frame: { $case: "registered", value: { supportsUnknownNameRejection: true, maxConcurrentTasksPerAppWorker: 0, taskQueue: "" } } }));
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
      call.on("data", () => call.write({ frame: { $case: "registered", value: { supportsUnknownNameRejection: true, maxConcurrentTasksPerAppWorker: 0, taskQueue: "" } } }));
    },
  } as WorkerTaskServiceServer);
  flowServer.addService(FlowServiceService, {
    getServerInfo(_call, callback: sendUnaryData<ServerInfo>) {
      calls.push("GetServerInfo");
      callback(null, {
        serverVersion: "server-zd/v0.1.16",
        minimumSupportedProtocolVersion: 1,
        currentProtocolVersion: 3,
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
      assert.match(failure.message, /The Server reports protocol versions 1-3 \(server version server-zd\/v0.1.16\)/);
      assert.match(failure.message, /protocol version 4 or later \(server-zd newer than v0.1.16\)/);
      assert.match(failure.message, /Upgrade the Server before upgrading the SDK/);
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
      call.on("data", () => call.write({ frame: { $case: "registered", value: { supportsUnknownNameRejection: true, maxConcurrentTasksPerAppWorker: 0, taskQueue: "" } } }));
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

test("Worker rejects an invalid task queue name at construction", () => {
  for (const taskQueue of ["dex.default", "dex.other", "has space", "q".repeat(64), "ü"]) {
    assert.throws(
      () => new Worker(new Registry([new IndexedFlow()]), new MemoryBlobCache(), { pull: { taskQueue } }),
      TypeError,
    );
  }
});

test("Worker with a task queue refuses a Server older than protocol 5 before sync and task streams", async () => {
  const fake = await startTaskQueueServer(4, (requested) => requested);
  const worker = new Worker(new Registry([new IndexedFlow()]), new MemoryBlobCache(), {
    serverAddress: `127.0.0.1:${fake.port}`, pull: { taskQueue: "blue" },
  });
  try {
    await assert.rejects(worker.start(), (failure: Error) => {
      assert.match(failure.message, /a pull Worker with task queue "blue" requires Server protocol version 5 or later; negotiated protocol version is 4/);
      return true;
    });
    assert.deepEqual(fake.calls, ["GetServerInfo"]);
    assert.deepEqual(fake.registeredTaskQueues, []);
  } finally {
    await worker.close();
    await shutdown(fake.server);
  }
});

test("Worker registers its task queue on every stream and reports it in readiness", async () => {
  const fake = await startTaskQueueServer(5, (requested) => requested);
  const worker = new Worker(new Registry([new IndexedFlow()]), new MemoryBlobCache(), {
    serverAddress: `127.0.0.1:${fake.port}`, pull: { taskQueue: "blue", maxConcurrentTasks: 2 },
  });
  try {
    await worker.start();
    await worker.waitPullRegistered({ timeoutMs: 5_000 });
    assert.deepEqual(fake.registeredTaskQueues, ["blue", "blue"]);
    const state = worker.pullRegistrationState();
    assert.equal(state.isReady, true);
    assert.deepEqual(state.queues.map((entry) => [entry.queue, entry.taskQueue]), [
      ["STEP_AND_RPC", "blue"], ["RPC_ONLY", "blue"],
    ]);
  } finally {
    await worker.close();
    await shutdown(fake.server);
  }
});

test("Worker half-closes a stream registered on a different task queue as a permanent rejection", async () => {
  const fake = await startTaskQueueServer(5, () => "");
  const worker = new Worker(new Registry([new IndexedFlow()]), new MemoryBlobCache(), {
    serverAddress: `127.0.0.1:${fake.port}`, pull: { taskQueue: "blue", maxConcurrentTasks: 2 },
  });
  const isTaskQueueRejection = (failure: unknown) => {
    assert.ok(failure instanceof PullRegistrationError);
    assert.equal(failure.taskQueue, "blue");
    assert.equal((failure.cause as ServiceError).code, grpcStatus.FAILED_PRECONDITION);
    assert.match(failure.message, /on task queue blue rejected: .*registered task queue "" instead of "blue"/);
    return true;
  };
  try {
    const waiting = worker.waitPullRegistered({ timeoutMs: 5_000 });
    waiting.catch(() => {});
    await assert.rejects(worker.start(), isTaskQueueRejection);
    await assert.rejects(waiting, isTaskQueueRejection);
    const state = worker.pullRegistrationState();
    assert.equal(state.isReady, false);
    for (const entry of state.queues) {
      assert.equal(entry.taskQueue, "blue");
      assert.equal(entry.hasRegisteredStream, false);
      assert.equal(entry.lastPermanentError?.code, grpcStatus.FAILED_PRECONDITION);
    }
    await until(() => fake.halfClosedStreams === 2);
    await new Promise((resolve) => setTimeout(resolve, 300));
    assert.deepEqual(fake.registeredTaskQueues, ["blue", "blue"]);
  } finally {
    await worker.close();
    await shutdown(fake.server);
  }
});

test("a running Worker treats a reconnect registered on a different task queue as a permanent rejection", async () => {
  let echoedTaskQueue = "blue";
  const fake = await startTaskQueueServer(5, () => echoedTaskQueue);
  const worker = new Worker(new Registry([new IndexedFlow()]), new MemoryBlobCache(), {
    serverAddress: `127.0.0.1:${fake.port}`, pull: { taskQueue: "blue", maxConcurrentTasks: 1 },
  });
  try {
    await worker.start();
    assert.equal(worker.pullRegistrationState().isReady, true);
    echoedTaskQueue = "";
    fake.endStreams();
    await until(() => worker.pullRegistrationState().queues[0]?.lastPermanentError !== undefined);
    const [entry] = worker.pullRegistrationState().queues;
    assert.equal(entry?.hasRegisteredStream, false);
    assert.equal(entry?.lastPermanentError?.code, grpcStatus.FAILED_PRECONDITION);
    assert.equal(worker.pullRegistrationState().isReady, false);
  } finally {
    await worker.close();
    await shutdown(fake.server);
  }
});

interface TaskQueueServer {
  readonly server: Server;
  readonly port: number;
  readonly calls: string[];
  readonly registeredTaskQueues: string[];
  readonly halfClosedStreams: number;
  endStreams(): void;
}

async function startTaskQueueServer(
  currentProtocolVersion: number,
  echoTaskQueue: (requested: string) => string,
): Promise<TaskQueueServer> {
  const calls: string[] = [];
  const registeredTaskQueues: string[] = [];
  const openCalls = new Set<{ end(): void }>();
  let halfClosedStreams = 0;
  const server = new Server();
  server.addService(WorkerTaskServiceService, {
    pollWorkerTask(call) {
      openCalls.add(call);
      call.on("end", () => {
        halfClosedStreams++;
        openCalls.delete(call);
        call.end();
      });
      call.on("error", () => openCalls.delete(call));
      call.on("data", (request: PollWorkerTaskRequest) => {
        if (request.frame?.$case !== "register") return;
        const requested = request.frame.value.taskQueue;
        registeredTaskQueues.push(requested);
        call.write({ frame: { $case: "registered", value: {
          supportsUnknownNameRejection: true, maxConcurrentTasksPerAppWorker: 0, taskQueue: echoTaskQueue(requested),
        } } });
      });
    },
  } as WorkerTaskServiceServer);
  server.addService(FlowServiceService, {
    getServerInfo(_call, callback: sendUnaryData<ServerInfo>) {
      calls.push("GetServerInfo");
      callback(null, { ...compatibleServerInfo(), currentProtocolVersion });
    },
    syncAttributeIndexes(_call, callback: sendUnaryData<SyncAttributeIndexResponse>) {
      calls.push("SyncAttributeIndexes");
      callback(null, {});
    },
  } as Partial<FlowServiceServer> as FlowServiceServer);
  const port = await bind(server);
  return {
    server, port, calls, registeredTaskQueues,
    get halfClosedStreams() { return halfClosedStreams; },
    endStreams() {
      for (const call of openCalls) call.end();
      openCalls.clear();
    },
  };
}

async function until(condition: () => boolean): Promise<void> {
  const deadline = Date.now() + 5_000;
  while (!condition()) {
    if (Date.now() >= deadline) throw new Error("timed out waiting for the fake Server");
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
}

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
    currentProtocolVersion: 4,
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
