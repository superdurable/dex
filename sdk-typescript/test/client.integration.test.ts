// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import test from "node:test";

import { BinaryWriter } from "@bufbuild/protobuf/wire";
import {
  Metadata,
  Server,
  ServerCredentials,
  status,
  type ServiceError,
  type sendUnaryData,
} from "@grpc/grpc-js";

import {
  Attribute,
  AttributeMatch,
  AttributeMap,
  Channel,
  ChannelMap,
  Client,
  DexServiceError,
  FlowDefinitionError,
  FlowNotFoundError,
  FlowTimeoutHandlerFailure,
  FlowTimeoutPolicy,
  Registry,
  Stream,
  StepList,
  ValueMappingError,
  doubleCodec,
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
  ErrorSubStatus,
  ServiceErrorResponse,
  Value,
  type FlowServiceServer,
  type GetAttributesRequest,
  type GetAttributesResponse,
  type InvokeRPCRequest,
  type InvokeRPCResponse,
  type LoadBlobsRequest,
  type LoadBlobsResponse,
  type ListStreamMessagesRequest,
  type ListStreamMessagesResponse,
  type ReadStreamRequest,
  type ReadStreamResponse,
  type SearchFlowsRequest,
  type StartFlowRequest,
  type StartFlowResponse,
  type UpdateFlowConfigRequest,
  type WaitForAttributeRequest,
  type WaitForAttributeResponse,
  type WaitForFlowRequest,
  type WaitForStepCompletionRequest,
  type WaitForStepCompletionResponse,
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
const revision = new Attribute("revision", doubleCodec);

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
      attributes: [revision, items],
      channels: [queued, byTenant],
      streams: [thinking],
    };
  }

  @rpc({
    inputCodec,
    outputCodec,
    lockAttributes: [revision.lock(), items.lock("tenant-a")],
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
    search?: SearchFlowsRequest;
    start?: StartFlowRequest;
    updateFlowConfig?: UpdateFlowConfigRequest;
    rpc?: InvokeRPCRequest;
    writeStream?: WriteStreamRequest;
    readStream?: ReadStreamRequest;
    listStreamMessages?: ListStreamMessagesRequest;
    waitForAttribute: WaitForAttributeRequest[];
    waitForStepCompletion: WaitForStepCompletionRequest[];
  } = { waitForAttribute: [], waitForStepCompletion: [] };
  const hydratedOutput = protoJson({ accepted: true });
  const server = new Server();
  server.addService(FlowServiceService, {
    searchFlows(call, callback) {
      requests.search = call.request as SearchFlowsRequest;
      callback(null, { flowRuns: [], nextPageToken: "" });
    },
    startFlow(call, callback: sendUnaryData<StartFlowResponse>) {
      requests.start = call.request as StartFlowRequest;
      callback(null, { runId: "run-1" });
    },
    updateFlowConfig(call, callback) {
      requests.updateFlowConfig = call.request as UpdateFlowConfigRequest;
      callback(null, {});
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
    listStreamMessages(call, callback: sendUnaryData<ListStreamMessagesResponse>) {
      requests.listStreamMessages = call.request as ListStreamMessagesRequest;
      callback(null, {
        messages: [
          {
            value: Value.create({ kind: { $case: "stringValue", value: "newest" } }),
            resumeToken: "resume-2",
            createdTime: new Date("2026-08-27T13:00:00.000Z"),
            source: "client-2",
          },
          {
            value: Value.create({ kind: { $case: "stringValue", value: "older" } }),
            resumeToken: "resume-1",
            createdTime: new Date("2026-08-27T12:00:00.000Z"),
            source: "client-1",
          },
        ],
        nextPageToken: "next-page",
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
      const entries = (call.request as LoadBlobsRequest).entries;
      assert.ok(entries.length > 0);
      assert.ok(entries.every((entry) => entry.flowId.length > 0));
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
        runAttempt: 0,
        requestId: "request-failed",
        flowType: "TestFlow",
        flowStatus: FlowStatus.FLOW_STATUS_FAILED,
        startTime: new Date(1_000),
        closeTime: new Date(2_000),
      });
    },
    waitForAttribute(
      call: { request: WaitForAttributeRequest },
      callback: sendUnaryData<WaitForAttributeResponse>,
    ) {
      requests.waitForAttribute.push(call.request);
      if (requests.waitForAttribute.length === 1) {
        callback(serviceError(
          status.DEADLINE_EXCEEDED,
          ErrorSubStatus.ERROR_SUB_STATUS_LONG_POLL_TIME_OUT,
        ));
        return;
      }
      callback(null, {
        matchedValue: Value.create({ kind: { $case: "doubleValue", value: 7 } }),
      });
    },
    waitForStepCompletion(
      call: { request: WaitForStepCompletionRequest },
      callback: sendUnaryData<WaitForStepCompletionResponse>,
    ) {
      requests.waitForStepCompletion.push(call.request);
      if (requests.waitForStepCompletion.length === 1) {
        callback(serviceError(
          status.DEADLINE_EXCEEDED,
          ErrorSubStatus.ERROR_SUB_STATUS_LONG_POLL_TIME_OUT,
        ));
        return;
      }
      callback(null, {});
    },
  } as Partial<FlowServiceServer> as FlowServiceServer);

  const port = await bind(server);
  const flow = new TestFlow();
  const cache = new MemoryBlobCache();
  const client = new Client(new Registry([flow]), cache, {
    serverAddress: `127.0.0.1:${port}`,
  });
  try {
    await client.searchFlows("WorkflowId = 'flow-1'", 1);
    assert.equal(requests.search?.includeContinuedAsNew, false);
    await client.searchFlows("WorkflowId = 'flow-1'", 1, "next", { includeContinuedAsNew: true });
    assert.equal(requests.search?.includeContinuedAsNew, true);
    assert.equal(requests.search?.nextPageToken, "next");
    assert.equal(requests.search?.query, "WorkflowId = 'flow-1'");
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
    assert.deepEqual(
      await client.invokeRPCWithOptions(
        flow.accept,
        "flow-1",
        { message: "hello" },
        {
          lockAttributeMapInstances: [items.lock("tenant-b"), items.lock("tenant-a")],
          loadAttributeMapInstances: [items.load("tenant-b"), items.load("tenant-a")],
          loadChannelMapInstances: [
            byTenant.loadMessages("tenant-b"),
            byTenant.loadMessages("tenant-a"),
          ],
        },
      ),
      { accepted: true },
    );
    await assert.rejects(
      client.invokeRPCWithOptions(
        flow.accept,
        "flow-1",
        { message: "hello" },
        { lockAttributeMapInstances: [revision.lock()] },
      ),
      /must target an AttributeMap instance/,
    );
    const foreignItems = new AttributeMap("foreign-items", stringCodec);
    await assert.rejects(
      client.invokeRPCWithOptions(
        flow.accept,
        "flow-1",
        { message: "hello" },
        { loadAttributeMapInstances: [foreignItems.load("tenant-a")] },
      ),
      /does not register AttributeMap/,
    );
    await client.writeStream("flow-1", thinking, "client-1", "starting");
    const message = await client.readStream("flow-1", thinking, "previous", 2_000);
    assert.equal(message.value, "working");
    assert.equal(message.resumeToken, "resume-1");
    assert.equal(message.createdTime.toISOString(), "2026-08-27T12:00:00.000Z");
    assert.equal(message.source, "client-1");
    const page = await client.listStreamMessages("flow-1", thinking, 2, "before-page");
    assert.deepEqual(page.messages.map((entry) => entry.value), ["newest", "older"]);
    assert.equal(page.messages[0]?.resumeToken, "resume-2");
    assert.equal(page.nextPageToken, "next-page");
    await assert.rejects(
      client.listStreamMessages("flow-1", thinking, 0),
      /page size must be a positive 32-bit integer/,
    );
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
    const attributeOptions = {};
    assert.equal(
      await client.waitForAttributeMatch(
        "flow-1",
        revision,
        AttributeMatch.greaterThan(5),
        attributeOptions,
      ),
      7,
    );
    assert.deepEqual(
      requests.waitForAttribute.map((request) => request.requestId),
      ["", ""],
    );
    const stepOptions = {};
    await client.waitForStepCompletion("flow-1", { stepType: "Start", number: 1 }, stepOptions);
    assert.deepEqual(
      requests.waitForStepCompletion.map((request) => request.requestId),
      ["", ""],
    );
    await client.waitForStepCompletion(
      "flow-1",
      { stepType: "Start" },
      { requestTimeoutMs: 1_000, internalHandlerTimeoutMs: 3_000 },
    );
    assert.equal(requests.waitForStepCompletion.at(-1)?.requestId, "");
    assert.equal(requests.waitForStepCompletion.at(-1)?.requestTimeoutSeconds, 1);
    assert.equal(requests.waitForStepCompletion.at(-1)?.internalHandlerTimeoutSeconds, 3);
    assert.deepEqual(requests.start?.flowStartOptions?.flowConfigOverride?.workerTarget, { isPullMode: true });
    await client.updateFlowConfig("flow-1", {});
    assert.ok(requests.updateFlowConfig?.flowConfig);
    assert.equal(requests.updateFlowConfig.flowConfig.workerTarget, undefined);
    await client.updateFlowConfig("flow-1", { continueAsNewThreshold: 123 });
    assert.equal(requests.updateFlowConfig.flowConfig.workerTarget, undefined);
    assert.equal(requests.updateFlowConfig.flowConfig.continueAsNewThreshold, 123);
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
    assert.deepEqual(requests.rpc?.lockAttributeKeys, [
      "items/tenant-a",
      "items/tenant-b",
      "revision",
    ]);
    assert.deepEqual(requests.rpc?.loadAttributeMapInstances, [
      "items/",
      "items/tenant-a",
      "items/tenant-b",
    ]);
    assert.deepEqual(requests.rpc?.loadChannelNames, ["queued"]);
    assert.deepEqual(requests.rpc?.loadChannelMapInstances, [
      "by-tenant/",
      "by-tenant/tenant-a",
      "by-tenant/tenant-b",
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
    assert.equal(requests.listStreamMessages?.pageSize, 2);
    assert.equal(requests.listStreamMessages?.beforePageToken, "before-page");
    assert.equal(cache.get("6:flow-1blob-1") === undefined, false);
    assert.equal(cache.get("6:flow-1blob-2") === undefined, false);
  } finally {
    await client.close();
    await shutdown(server);
  }
});

test("Client getAttributes sends one request, hydrates references, and validates responses", async () => {
  const getAttributesRequests: GetAttributesRequest[] = [];
  const loadBlobsRequests: LoadBlobsRequest[] = [];
  const responses: Record<string, GetAttributesResponse> = {
    "flow-1": {
      attributes: [
        { key: "revision", value: Value.create({ kind: { $case: "doubleValue", value: 0 } }) },
        {
          key: "items/tenant%20a",
          value: Value.create({ kind: { $case: "internalBlobIdForStringValue", value: "blob-3" } }),
        },
      ],
    },
    unexpected: { attributes: [{ key: "other", value: protoJson(1) }] },
    duplicate: {
      attributes: [
        { key: "revision", value: Value.create({ kind: { $case: "doubleValue", value: 1 } }) },
        { key: "revision", value: Value.create({ kind: { $case: "doubleValue", value: 2 } }) },
      ],
    },
    "missing-value": { attributes: [{ key: "revision", value: undefined }] },
    "blob-error": {
      attributes: [{
        key: "revision",
        value: Value.create({ kind: { $case: "internalBlobIdForObjValue", value: "blob-error" } }),
      }],
    },
    "blob-missing": {
      attributes: [{
        key: "revision",
        value: Value.create({ kind: { $case: "internalBlobIdForObjValue", value: "blob-missing" } }),
      }],
    },
    "json-null": { attributes: [{ key: "revision", value: protoJson(null) }] },
    "bad-value": {
      attributes: [{ key: "revision", value: Value.create({ kind: { $case: "stringValue", value: "x" } }) }],
    },
  };
  const server = new Server();
  server.addService(FlowServiceService, {
    getAttributes(call, callback: sendUnaryData<GetAttributesResponse>) {
      const request = call.request as GetAttributesRequest;
      getAttributesRequests.push(request);
      const response = responses[request.flowId];
      if (response === undefined) {
        callback(serviceError(status.NOT_FOUND, ErrorSubStatus.ERROR_SUB_STATUS_FLOW_NOT_EXISTS));
        return;
      }
      callback(null, response);
    },
    loadBlobs(call, callback: sendUnaryData<LoadBlobsResponse>) {
      const request = call.request as LoadBlobsRequest;
      loadBlobsRequests.push(request);
      if (request.entries[0]?.flowId === "blob-error") {
        callback(Object.assign(new Error("blob store unavailable"), {
          code: status.UNAVAILABLE,
          details: "blob store unavailable",
          metadata: new Metadata(),
        }));
        return;
      }
      callback(null, { values: { "blob-3": Value.create({ kind: { $case: "stringValue", value: "hydrated" } }) } });
    },
  } as Partial<FlowServiceServer> as FlowServiceServer);

  const port = await bind(server);
  const cache = new MemoryBlobCache();
  const client = new Client(new Registry([new TestFlow()]), cache, {
    serverAddress: `127.0.0.1:${port}`,
  });
  try {
    const [revisionValue, itemA, itemB] = await client.getAttributes(
      "flow-1",
      revision,
      items.instance("tenant a"),
      items.instance("tenant-b"),
    );
    assert.equal(revisionValue, 0);
    assert.equal(itemA, "hydrated");
    assert.equal(itemB, undefined);
    assert.deepEqual(getAttributesRequests, [{
      flowId: "flow-1",
      runId: "",
      keys: ["revision", "items/tenant%20a", "items/tenant-b"],
      allKeys: false,
    }]);
    assert.equal(loadBlobsRequests.length, 1);
    assert.equal(loadBlobsRequests[0]?.entries[0]?.flowId, "flow-1");
    const [, cachedItemA] = await client.getAttributes("flow-1", revision, items.instance("tenant a"));
    assert.equal(cachedItemA, "hydrated");
    assert.equal(getAttributesRequests.length, 2);
    assert.equal(loadBlobsRequests.length, 1, "the second read uses the BlobCache");

    await assert.rejects(
      client.getAttributes("blob-error", revision),
      (error) => error instanceof DexServiceError
        && error.code === status.UNAVAILABLE
        && error.detail === "blob store unavailable"
        && error.flowId === "blob-error"
        && error.operation === "loadBlobs"
        && error.message === "getAttributes Flow blob-error Attributes revision LoadBlobs: blob store unavailable",
    );
    await assert.rejects(
      client.getAttributes("blob-missing", revision),
      (error) => error instanceof ValueMappingError
        && error.operation === "hydrate"
        && /getAttributes Flow blob-missing Attributes revision: Dex did not hydrate blob blob-missing/.test(error.message),
    );

    const nullableRevision = new Attribute("revision", jsonCodec<number | null>());
    const nullableClient = new Client(new Registry([new NullableRevisionFlow(nullableRevision)]), cache, {
      serverAddress: `127.0.0.1:${port}`,
    });
    try {
      const [nullRevision] = await nullableClient.getAttributes("json-null", nullableRevision);
      assert.equal(nullRevision, null);
    } finally {
      await nullableClient.close();
    }

    const requestCountBeforeRejections = getAttributesRequests.length;
    const sameKeyRead = new AttributeMap("items", stringCodec);
    await assert.rejects(
      client.getAttributes("flow-1", revision, revision),
      (error) => error instanceof TypeError && /Flow flow-1 has duplicate Attribute read revision/.test(error.message),
    );
    await assert.rejects(
      client.getAttributes("flow-1", items.instance("tenant-b"), items.instance("tenant-b")),
      /Flow flow-1 has duplicate Attribute read items\/tenant-b/,
    );
    await assert.rejects(
      client.getAttributes("flow-1", sameKeyRead.instance("tenant-b")),
      (error) => error instanceof FlowDefinitionError
        && /Flow flow-1: AttributeMap items differs from the definition registered by Flow TestFlow/.test(error.message),
    );
    await assert.rejects(
      client.getAttributes("flow-1", new Attribute("unregistered", stringCodec)),
      (error) => error instanceof FlowDefinitionError
        && /Flow flow-1: Attribute unregistered is not registered by any Flow/.test(error.message),
    );
    await assert.rejects(
      client.getAttributes("", revision),
      (error) => error instanceof TypeError && /non-empty Flow ID/.test(error.message),
    );
    const noReads: readonly [] = [];
    await assert.rejects(
      // @ts-expect-error getAttributes requires at least one read.
      client.getAttributes("flow-1", ...noReads),
      /Flow flow-1 requires at least one Attribute read/,
    );
    await assert.rejects(
      // @ts-expect-error An AttributeMap read requires an instance selection.
      client.getAttributes("flow-1", items),
      /Flow flow-1 AttributeMap items requires an instance/,
    );
    assert.equal(getAttributesRequests.length, requestCountBeforeRejections);

    await assert.rejects(
      client.getAttributes("unexpected", revision),
      /Flow unexpected returned unexpected Attribute other/,
    );
    await assert.rejects(
      client.getAttributes("duplicate", revision),
      /Flow duplicate returned duplicate Attribute revision/,
    );
    await assert.rejects(
      client.getAttributes("missing-value", revision),
      /Flow missing-value returned Attribute revision without a value/,
    );
    await assert.rejects(
      client.getAttributes("bad-value", revision),
      (error) => error instanceof ValueMappingError
        && error.operation === "decode"
        && /getAttributes Flow bad-value Attribute revision: expected double, got string/.test(error.message),
    );
    await assert.rejects(
      client.getAttributes("missing", revision, items.instance("tenant-a")),
      (error) => error instanceof FlowNotFoundError
        && error.flowId === "missing"
        && error.operation === "getAttributes"
        && error.message.startsWith("getAttributes Flow missing Attributes revision, items/tenant-a: "),
    );
  } finally {
    await client.close();
    await shutdown(server);
  }
});

class ChannelOnlyFlow implements Flow<void> {
  public getFlowType(): string {
    return "ChannelOnlyFlow";
  }

  public getSteps() {
    return StepList.empty();
  }

  public getPersistenceSchema() {
    return { channels: [new Channel("x", stringCodec)] };
  }
}

test("Client getAttributes does not treat a same-named Channel as a conflicting Attribute", async () => {
  const client = new Client(new Registry([new ChannelOnlyFlow()]), new MemoryBlobCache(), {
    serverAddress: "127.0.0.1:1",
  });
  try {
    await assert.rejects(
      client.getAttributes("flow-1", new Attribute("x", stringCodec)),
      (error) => error instanceof FlowDefinitionError
        && error.message === "getAttributes Flow flow-1: Attribute x is not registered by any Flow in the Registry",
    );
  } finally {
    await client.close();
  }
});

class NullableRevisionFlow implements Flow<void> {
  public constructor(private readonly revision: Attribute<number | null>) {}

  public getFlowType(): string {
    return "NullableRevisionFlow";
  }

  public getSteps() {
    return StepList.empty();
  }

  public getPersistenceSchema() {
    return { attributes: [this.revision] };
  }
}

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

function serviceError(code: status, subStatus: ErrorSubStatus): ServiceError {
  const response = ServiceErrorResponse.encode({
    detail: "long poll timed out",
    subStatus,
    originalWorkerErrorDetail: "",
    originalWorkerErrorType: "",
    originalWorkerErrorStatus: 0,
    originalWorkerErrorStackTrace: "",
    dispatchTaskErrorReason: 0,
  }).finish();
  const writer = new BinaryWriter();
  writer.uint32(26).fork();
  writer.uint32(10).string("type.googleapis.com/dex.ServiceErrorResponse");
  writer.uint32(18).bytes(response);
  writer.join();
  const metadata = new Metadata();
  metadata.set("grpc-status-details-bin", Buffer.from(writer.finish()));
  return Object.assign(new Error("long poll timed out"), {
    code,
    details: "long poll timed out",
    metadata,
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
