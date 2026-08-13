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
  FlowUncompletedError,
  Registry,
  StepList,
  jsonCodec,
  rpc,
  stringCodec,
  type BlobCache,
  type Context,
  type Flow,
  type RPCResult,
  type Step,
  type StepDecision,
} from "../src/index.js";
import {
  FlowServiceService,
  FlowErrorType as ProtoFlowErrorType,
  type FlowResult as ProtoFlowResult,
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
      call: { request: WaitForFlowRequest },
      callback: sendUnaryData<ProtoFlowResult>,
    ) {
      const results = [
        {
          completedStepType: "Start",
          completedStepExecutionId: "Start-1",
          completedStepOutput: Value.create({
            kind: { $case: "internalBlobIdForObjValue" as const, value: "blob-1" },
          }),
        },
        {
          completedStepType: "Finish",
          completedStepExecutionId: "Finish-2",
          completedStepOutput: Value.create({
            kind: { $case: "internalBlobIdForStringValue" as const, value: "blob-2" },
          }),
        },
      ];
      callback(null, {
        flowStatus: call.request.flowId === "failed"
          ? FlowStatus.FLOW_STATUS_FAILED
          : FlowStatus.FLOW_STATUS_COMPLETED,
        results: call.request.flowId === "empty"
          ? []
          : call.request.flowId === "single"
            ? results.slice(0, 1)
            : results,
        errorType: call.request.flowId === "failed"
          ? ProtoFlowErrorType.FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW
          : ProtoFlowErrorType.FLOW_ERROR_TYPE_UNSPECIFIED,
        errorMessage: call.request.flowId === "failed" ? "failed by test" : "",
      });
    },
    loadBlobs(call, callback: sendUnaryData<LoadBlobsResponse>) {
      assert.equal((call.request as LoadBlobsRequest).values.length, 2);
      callback(null, {
        values: {
          "blob-1": hydratedOutput,
          "blob-2": Value.create({ kind: { $case: "stringValue", value: "done" } }),
        },
      });
    },
    getFlowSummary(_call, callback) {
      callback(null, {
        flowExecutionId: { flowId: "failed", runId: "run-failed" },
        firstRunId: "run-failed",
        requestId: "request-failed",
        flowType: "TestFlow",
        flowStatus: FlowStatus.FLOW_STATUS_FAILED,
        startTime: new Date(1_000),
        closeTime: new Date(2_000),
      });
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
    const result = await client.waitForFlow("flow-1");
    assert.equal(result.completions.length, 2);
    assert.equal(result.completions[0]?.stepType, "Start");
    assert.equal(result.completions[0]?.stepExecutionId, "Start-1");
    assert.deepEqual(result.completions[0]?.decode(outputCodec), { accepted: true });
    assert.equal(result.completions[1]?.decode(stringCodec), "done");
    assert.throws(() => result.singleOutput(outputCodec), /exactly one Step output/);
    assert.deepEqual((await client.waitForFlow("single")).singleOutput(outputCodec), {
      accepted: true,
    });
    const empty = await client.waitForFlow("empty");
    assert.throws(() => empty.singleOutput(stringCodec), /found 0/);
    await assert.rejects(
      client.waitForFlow("failed"),
      (error: unknown) => {
        assert.ok(error instanceof FlowUncompletedError);
        assert.equal(error.runId, "run-failed");
        assert.equal(error.completions[1]?.stepExecutionId, "Finish-2");
        assert.equal(error.completions[1]?.decode(stringCodec), "done");
        return true;
      },
    );
    assert.equal(requests.start?.flowType, "TestFlow");
    assert.equal(requests.start?.startStepType, "Start");
    assert.equal(requests.rpc?.rpcName, "accept");
    assert.equal(cache.get("blob-1") === undefined, false);
    assert.equal(cache.get("blob-2") === undefined, false);
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
