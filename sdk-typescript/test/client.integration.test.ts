// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import assert from "node:assert/strict";
import test from "node:test";

import { Server, ServerCredentials, type sendUnaryData } from "@grpc/grpc-js";

import {
  Client,
  Registry,
  StepList,
  jsonCodec,
  rpc,
  type BlobCache,
  type Context,
  type Flow,
  type RPCResult,
  type Step,
  type StepDecision,
} from "../src/index.js";
import {
  FlowServiceService,
  FlowStatus,
  Value,
  type FlowServiceServer,
  type InvokeRPCRequest,
  type InvokeRPCResponse,
  type LoadBlobsRequest,
  type LoadBlobsResponse,
  type StartFlowRequest,
  type StartFlowResponse,
  type WaitForFlowRequest,
  type WaitForFlowResponse,
} from "../src/gen/dex.js";

interface Input {
  readonly message: string;
}

interface Output {
  readonly accepted: boolean;
}

const inputCodec = jsonCodec<Input>({
  typeName: "Input",
  decode: (value) => value as Input,
});
const outputCodec = jsonCodec<Output>({
  typeName: "Output",
  decode: (value) => value as Output,
});

class Start implements Step<Input> {
  public readonly inputCodec = inputCodec;

  public getStepType(): string {
    return "Start";
  }

  public execute(_context: Context, _input: Input): StepDecision {
    return { kind: "gracefulComplete", output: undefined };
  }
}

class TestFlow implements Flow<Input> {
  public readonly start = new Start();

  public getFlowType(): string {
    return "TestFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  @rpc({ inputCodec, outputCodec })
  public accept(_context: Context, _input: Input): RPCResult<Output> {
    return { output: { accepted: true } };
  }
}

test("Client maps typed calls and hydrates blob-backed outputs", async () => {
  const requests: { start?: StartFlowRequest; rpc?: InvokeRPCRequest } = {};
  const hydratedOutput = protoJson({ accepted: true });
  const server = new Server();
  server.addService(FlowServiceService, {
    startFlow(call, callback: sendUnaryData<StartFlowResponse>) {
      requests.start = call.request as StartFlowRequest;
      callback(null, { runId: "run-1" });
    },
    invokeRpc(call, callback: sendUnaryData<InvokeRPCResponse>) {
      requests.rpc = call.request as InvokeRPCRequest;
      callback(null, { output: hydratedOutput });
    },
    waitForFlow(
      _call: { request: WaitForFlowRequest },
      callback: sendUnaryData<WaitForFlowResponse>,
    ) {
      callback(null, {
        flowStatus: FlowStatus.FLOW_STATUS_COMPLETED,
        results: [
          {
            completedStepType: "Start",
            completedStepExecutionId: "Start-1",
            completedStepOutput: Value.create({
              kind: { $case: "internalBlobIdForObjValue", value: "blob-1" },
            }),
          },
        ],
        errorType: 0,
        errorMessage: "",
      });
    },
    loadBlobs(call, callback: sendUnaryData<LoadBlobsResponse>) {
      assert.equal((call.request as LoadBlobsRequest).values.length, 1);
      callback(null, { values: { "blob-1": hydratedOutput } });
    },
  } as Partial<FlowServiceServer> as FlowServiceServer);

  const port = await bind(server);
  const flow = new TestFlow();
  const cache = new MemoryBlobCache();
  const client = new Client(new Registry([flow]), cache, {
    serverAddress: `127.0.0.1:${port}`,
  });
  try {
    assert.equal(await client.startFlow(flow, "flow-1", { message: "hello" }), "run-1");
    assert.deepEqual(await client.invokeRPC(flow.accept, "flow-1", { message: "hello" }), {
      accepted: true,
    });
    assert.deepEqual(await client.waitForFlow("flow-1", outputCodec), { accepted: true });
    assert.equal(requests.start?.flowType, "TestFlow");
    assert.equal(requests.start?.startStepType, "Start");
    assert.equal(requests.rpc?.rpcName, "accept");
    assert.equal(cache.get("blob-1") === undefined, false);
  } finally {
    await client.close();
    await shutdown(server);
  }
});

class MemoryBlobCache implements BlobCache {
  public readonly config = { directory: "memory", maxBytes: 1_024 };
  private readonly values = new Map<string, Uint8Array>();

  public get(blobId: string): Uint8Array | undefined {
    return this.values.get(blobId);
  }

  public put(blobId: string, payload: Uint8Array): boolean {
    this.values.set(blobId, payload);
    return true;
  }

  public delete(blobId: string): void {
    this.values.delete(blobId);
  }

  public deleteAll(): void {
    this.values.clear();
  }

  public close(): void {}
}

function protoJson(value: unknown): Value {
  return Value.create({
    kind: {
      $case: "objValue",
      value: { encoding: "json", payload: new TextEncoder().encode(JSON.stringify(value)) },
    },
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
    server.tryShutdown((error) => {
      if (error !== undefined) {
        reject(error);
        return;
      }
      resolve();
    });
  });
}
