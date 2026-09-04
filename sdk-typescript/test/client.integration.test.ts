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
  AttributeMap,
  Channel,
  ChannelMap,
  Client,
  FlowTimeoutHandlerFailure,
  FlowTimeoutPolicy,
  Registry,
  Stream,
  StepList,
  jsonCodec,
  rpc,
  stringCodec,
  voidCodec,
  type BlobCache,
  type AsyncContext,
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
  type ReadStreamRequest,
  type ReadStreamResponse,
  type StartFlowRequest,
  type StartFlowResponse,
  type WaitForFlowRequest,
  type WriteStreamRequest,
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
const thinking = new Stream("thinking", stringCodec, 1_048_576);
const items = new AttributeMap("items", stringCodec);
const queued = new Channel("queued", stringCodec);
const byTenant = new ChannelMap("by-tenant", stringCodec);

class Start implements Step<Input> {
  public readonly inputCodec = inputCodec;

  public getStepType(): string {
    return "Start";
  }

  public getStepOptions() {
    return { heartbeatTimeoutMs: 2_000 };
  }

  public execute(_context: Context, _input: Input): StepDecision {
    return { kind: "gracefulComplete", output: undefined };
  }
}

class TimeoutRecovery implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "TimeoutRecovery";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return { kind: "deadEnd" };
  }
}

class TestFlow implements Flow<Input> {
  public readonly start = new Start();
  public readonly timeoutRecovery = new TimeoutRecovery();

  public getFlowType(): string {
    return "TestFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start).otherSteps(this.timeoutRecovery);
  }

  public getPersistenceSchema() {
    return {
      attributes: [items],
      channels: [queued, byTenant],
      streams: [thinking],
    };
  }

  @rpc({
    inputCodec,
    outputCodec,
    loadAttributeMaps: [items],
    loadAttributeMapInstances: [items.load("tenant-a")],
    loadChannels: [queued],
    loadChannelMaps: [byTenant],
    loadChannelMapInstances: [byTenant.loadMessages("tenant-a")],
  })
  public accept(_context: Context, _input: Input): RPCResult<Output> {
    return { output: { accepted: true } };
  }

  public handleTimeout(_context: AsyncContext): StepDecision {
    return { kind: "deadEnd" };
  }
}

test("Client maps typed calls and hydrates blob-backed outputs", async () => {
  const requests: {
    start?: StartFlowRequest;
    rpc?: InvokeRPCRequest;
    writeStream?: WriteStreamRequest;
    readStream?: ReadStreamRequest;
  } = {};
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
    writeStream(call, callback) {
      requests.writeStream = call.request as WriteStreamRequest;
      callback(null, {});
    },
    readStream(call, callback: sendUnaryData<ReadStreamResponse>) {
      requests.readStream = call.request as ReadStreamRequest;
      callback(null, {
        message: {
          value: Value.create({ kind: { $case: "stringValue", value: "working" } }),
          resumeToken: "resume-1",
          createdTime: new Date("2026-08-27T12:00:00.000Z"),
          source: "client-1",
        },
      });
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
    assert.equal(await client.startFlow(flow, "flow-1", { message: "hello" }, {
      timeoutMs: 30_000,
      timeoutPolicy: FlowTimeoutPolicy.HANDLER,
      timeoutHandlerOptions: {
        methodTimeoutMs: 10_000,
        heartbeatTimeoutMs: 5_000,
        retry: { maximumAttempts: 3 },
        failure: FlowTimeoutHandlerFailure.proceedTo(TimeoutRecovery),
        durability: "async",
        loadAttributeMaps: [items],
        loadAttributeMapInstances: [items.load("tenant-a")],
        loadChannels: [queued],
        loadChannelMaps: [byTenant],
        loadChannelMapInstances: [byTenant.loadMessages("tenant-a")],
      },
    }), "run-1");
    assert.deepEqual(await client.invokeRPC(flow.accept, "flow-1", { message: "hello" }), {
      accepted: true,
    });
    await client.writeStream("flow-1", thinking, "client-1", "starting");
    const message = await client.readStream("flow-1", thinking, "previous", 2_000);
    assert.equal(message.value, "working");
    assert.equal(message.resumeToken, "resume-1");
    assert.equal(message.createdTime.toISOString(), "2026-08-27T12:00:00.000Z");
    assert.equal(message.source, "client-1");
    await client.writeStream("flow-1", thinking, "source#with-delimiter", "accepted");
    await assert.rejects(client.writeStream("flow-1", thinking, "", "ignored"), /source is required/);
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
    const failed = await client.waitForFlow("failed");
    assert.equal(failed.status, "failed");
    assert.equal(failed.completions[1]?.stepExecutionId, "Finish-2");
    assert.equal(failed.completions[1]?.decode(stringCodec), "done");
    assert.equal(requests.start?.flowType, "TestFlow");
    assert.equal(requests.start?.startStepType, "Start");
    assert.equal(requests.start?.stepOptions?.heartbeatTimeoutSeconds, 2);
    assert.equal(requests.start?.flowStartOptions?.timeoutHandlerOptions?.methodTimeoutSeconds, 10);
    assert.equal(requests.start?.flowStartOptions?.timeoutHandlerOptions?.heartbeatTimeoutSeconds, 5);
    assert.equal(requests.start?.flowStartOptions?.timeoutHandlerOptions?.retryPolicy?.maximumAttempts, 3);
    assert.equal(requests.start?.flowStartOptions?.timeoutHandlerOptions?.failureProceedStepType, "TimeoutRecovery");
    assert.equal(requests.start?.flowStartOptions?.timeoutHandlerOptions?.failureProceedStepOptions?.skipWaitFor, true);
    assert.deepEqual(
      requests.start?.flowStartOptions?.timeoutHandlerOptions?.loadAttributeMapInstances,
      ["items/", "items/tenant-a"],
    );
    assert.deepEqual(
      requests.start?.flowStartOptions?.timeoutHandlerOptions?.loadChannelNames,
      ["queued"],
    );
    assert.deepEqual(
      requests.start?.flowStartOptions?.timeoutHandlerOptions?.loadChannelMapInstances,
      ["by-tenant/", "by-tenant/tenant-a"],
    );
    assert.equal(requests.rpc?.rpcName, "accept");
    assert.deepEqual(requests.rpc?.loadAttributeMapInstances, ["items/", "items/tenant-a"]);
    assert.deepEqual(requests.rpc?.loadChannelNames, ["queued"]);
    assert.deepEqual(requests.rpc?.loadChannelMapInstances, [
      "by-tenant/",
      "by-tenant/tenant-a",
    ]);
    assert.deepEqual(requests.writeStream, {
      flowId: "flow-1",
      flowType: "TestFlow",
      streamName: "thinking",
      streamCapacityBytes: 1_048_576n,
      value: Value.create({ kind: { $case: "stringValue", value: "accepted" } }),
      source: "source#with-delimiter",
    });
    assert.equal(requests.readStream?.flowType, "TestFlow");
    assert.equal(requests.readStream?.resumeToken, "previous");
    assert.equal(requests.readStream?.waitTimeSeconds, 2);
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
