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
  Client,
  FlowAlreadyStartedError,
  FlowNotFoundError,
  IdReusePolicy,
  InitialAttribute,
  Registry,
  StepList,
  SubFlowResetPolicy,
  TimeTravelStepMethod,
  TimeTravelType,
  stringCodec,
  type BlobCache,
  type CloneForkFlowOptions,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
  type TimeTravelOptions,
} from "../src/index.js";
import {
  ErrorSubStatus,
  FlowCloneForkPointType,
  FlowResetStepMethod,
  FlowResetType,
  FlowServiceService,
  IdReusePolicy as ProtoIdReusePolicy,
  ServiceErrorResponse,
  SubFlowResetPolicy as ProtoSubFlowResetPolicy,
  type CloneForkFlowRequest,
  type CloneForkFlowResponse,
  type FlowServiceServer,
  type ResetFlowRequest,
  type ResetFlowResponse,
  type ServerInfo,
} from "../src/gen/dex.js";

const label = new Attribute("label", stringCodec);

class CloneStart implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "CloneStart";
  }

  public execute(_context: Context, _input: string): StepDecision {
    return { kind: "deadEnd" };
  }
}

class CloneSessionFlow implements Flow<string> {
  public getFlowType(): string {
    return "CloneSessionFlow";
  }

  public getSteps() {
    return StepList.startStep(new CloneStart());
  }

  public getPersistenceSchema() {
    return { attributes: [label] };
  }
}

interface FakeService {
  readonly client: Client;
  readonly flow: CloneSessionFlow;
  readonly cloneRequests: CloneForkFlowRequest[];
  readonly resetRequests: ResetFlowRequest[];
  failure: ServiceError | undefined;
  close(): Promise<void>;
}

async function withFakeService(
  currentProtocolVersion: number,
  run: (fake: FakeService) => Promise<void>,
): Promise<void> {
  const cloneRequests: CloneForkFlowRequest[] = [];
  const resetRequests: ResetFlowRequest[] = [];
  const server = new Server();
  const fake = { failure: undefined as ServiceError | undefined };
  server.addService(FlowServiceService, {
    getServerInfo(_call, callback: sendUnaryData<ServerInfo>) {
      callback(null, { serverVersion: "test", minimumSupportedProtocolVersion: 1, currentProtocolVersion, serverEngine: 0 });
    },
    cloneForkFlow(call, callback: sendUnaryData<CloneForkFlowResponse>) {
      cloneRequests.push(call.request as CloneForkFlowRequest);
      if (fake.failure !== undefined) {
        callback(fake.failure);
        return;
      }
      callback(null, { runId: "clone-run-1", sourceRunId: "source-run-1", sourceRunAttempt: 2, sourceEventId: 17n });
    },
    resetFlow(call, callback: sendUnaryData<ResetFlowResponse>) {
      resetRequests.push(call.request as ResetFlowRequest);
      callback(null, { runId: "run-1", runAttempt: 2 });
    },
  } as Partial<FlowServiceServer> as FlowServiceServer);
  const port = await new Promise<number>((resolve, reject) => {
    server.bindAsync("127.0.0.1:0", ServerCredentials.createInsecure(), (error, boundPort) => {
      if (error !== null) {
        reject(error);
        return;
      }
      resolve(boundPort);
    });
  });
  const flow = new CloneSessionFlow();
  const client = new Client(new Registry([flow]), new MemoryBlobCache(), { serverAddress: `127.0.0.1:${port}` });
  const state: FakeService = {
    client,
    flow,
    cloneRequests,
    resetRequests,
    get failure() {
      return fake.failure;
    },
    set failure(value) {
      fake.failure = value;
    },
    close: async () => {
      await client.close();
      server.forceShutdown();
    },
  };
  try {
    await run(state);
  } finally {
    await state.close();
  }
}

test("cloneForkFlow maps defaults and the response", async () => {
  await withFakeService(6, async ({ client, flow, cloneRequests }) => {
    const result = await client.cloneForkFlow(flow, "source-1", "clone-1");
    assert.deepEqual(result, { runId: "clone-run-1", sourceRunId: "source-run-1", sourceRunAttempt: 2, sourceEventId: 17 });
    const request = cloneRequests[0]!;
    assert.equal(request.sourceFlowId, "source-1");
    assert.equal(request.flowId, "clone-1");
    assert.equal(request.sourceRunId, "");
    assert.match(request.requestId, /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
    assert.equal(request.pointType, FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_CURRENT);
    assert.equal(request.stepMethod, FlowResetStepMethod.FLOW_RESET_STEP_METHOD_UNSPECIFIED);
    assert.equal(request.subFlowResetPolicy, ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_UNSPECIFIED);
    assert.equal(request.idReusePolicy, ProtoIdReusePolicy.ID_REUSE_POLICY_UNSPECIFIED);
    assert.equal(request.flowConfigOverride, undefined, "no override keeps the source run's FlowConfig");
    assert.equal(request.flowAlreadyStartedOptions, undefined);
    assert.deepEqual(request.attributes, []);
    assert.equal(request.flowTimeoutSeconds, 0);
    assert.equal(request.runAttempt, 0);
    assert.equal(request.eventId, 0n);
    assert.equal(request.historyEventTime, "");

    await client.cloneForkFlow(flow, "source-1", "clone-2");
    assert.notEqual(cloneRequests[1]!.requestId, request.requestId, "each call sends a new request ID");
  });
});

test("cloneForkFlow maps every option", async () => {
  await withFakeService(6, async ({ client, flow, cloneRequests }) => {
    await client.cloneForkFlow(flow, "source-1", "clone-1", {
      sourceRunId: "source-run-9",
      sourceRunAttempt: 3,
      type: TimeTravelType.EVENT_ID,
      eventId: 42,
      idReusePolicy: IdReusePolicy.ALLOW_TERMINATE_IF_RUNNING,
      ignoreAlreadyStarted: true,
      requestId: "branch-request",
      configOverride: { historyWindowEvents: 64 },
      taskQueue: "tenant-b",
      attributes: [InitialAttribute.of(label, "copied")],
      timeoutMs: 90_000,
      reason: "branch the session",
      subFlowResetPolicy: SubFlowResetPolicy.ATTACH_ELSE_FAIL,
    });
    const request = cloneRequests[0]!;
    assert.equal(request.sourceRunId, "source-run-9");
    assert.equal(request.runAttempt, 3);
    assert.equal(request.pointType, FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_EVENT_ID);
    assert.equal(request.eventId, 42n);
    assert.equal(request.idReusePolicy, ProtoIdReusePolicy.ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING);
    assert.equal(request.flowAlreadyStartedOptions?.ignoreAlreadyStartedError, true);
    assert.equal(request.requestId, "branch-request");
    assert.equal(request.flowConfigOverride?.historyWindowEvents, 64);
    assert.equal(request.flowConfigOverride?.stepDurability, undefined, "unset fields keep the source run's values");
    assert.deepEqual(request.flowConfigOverride?.workerTarget, { isPullMode: true, taskQueue: "tenant-b" });
    assert.equal(request.attributes.length, 1);
    assert.equal(request.attributes[0]!.key, "label");
    assert.equal(request.flowTimeoutSeconds, 90);
    assert.equal(request.reason, "branch the session");
    assert.equal(request.subFlowResetPolicy, ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_FAIL);
  });
});

test("cloneForkFlow maps every point type", async () => {
  const cases: readonly [CloneForkFlowOptions, FlowCloneForkPointType, FlowResetStepMethod][] = [
    [{ type: TimeTravelType.CURRENT }, FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_CURRENT, FlowResetStepMethod.FLOW_RESET_STEP_METHOD_UNSPECIFIED],
    [{ type: TimeTravelType.BEGINNING, sourceRunAttempt: 1 }, FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_BEGINNING, FlowResetStepMethod.FLOW_RESET_STEP_METHOD_UNSPECIFIED],
    [
      { type: TimeTravelType.HISTORY_EVENT_TIME, historyEventTime: new Date("2026-11-15T10:30:00Z") },
      FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_HISTORY_EVENT_TIME,
      FlowResetStepMethod.FLOW_RESET_STEP_METHOD_UNSPECIFIED,
    ],
    [{ type: TimeTravelType.STEP_TYPE, stepType: "ChargeOrder" }, FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_STEP_TYPE, FlowResetStepMethod.FLOW_RESET_STEP_METHOD_UNSPECIFIED],
    [
      { type: TimeTravelType.STEP_EXECUTION_ID, stepExecutionId: "ChargeOrder-2", stepMethod: TimeTravelStepMethod.WAIT_FOR },
      FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_STEP_EXECUTION_ID,
      FlowResetStepMethod.FLOW_RESET_STEP_METHOD_WAIT_FOR,
    ],
    [{ type: TimeTravelType.EVENT_ID, eventId: 7 }, FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_EVENT_ID, FlowResetStepMethod.FLOW_RESET_STEP_METHOD_UNSPECIFIED],
  ];
  await withFakeService(6, async ({ client, flow, cloneRequests }) => {
    for (const [options, pointType, stepMethod] of cases) {
      await client.cloneForkFlow(flow, "source-1", "clone-1", options);
      const request = cloneRequests.at(-1)!;
      assert.equal(request.pointType, pointType);
      assert.equal(request.stepMethod, stepMethod);
      assert.equal(request.stepType, options.stepType ?? "");
      assert.equal(request.stepExecutionId, options.stepExecutionId ?? "");
      assert.equal(request.eventId, BigInt(options.eventId ?? 0));
      assert.equal(request.runAttempt, options.sourceRunAttempt ?? 0);
      assert.equal(request.historyEventTime, options.historyEventTime === undefined ? "" : "2026-11-15T10:30:00.000Z");
    }
  });
});

test("cloneForkFlow rejects invalid options before sending", async () => {
  const invalid: readonly [string, string, CloneForkFlowOptions, RegExp][] = [
    ["source-1", "clone-1", { eventId: 3 }, /type current cannot include another selector/],
    ["source-1", "clone-1", { sourceRunAttempt: 1 }, /CURRENT accepts only sourceRunAttempt zero/],
    ["source-1", "clone-1", { type: TimeTravelType.BEGINNING, sourceRunAttempt: -1 }, /non-negative int32/],
    ["source-1", "clone-1", { type: TimeTravelType.EVENT_ID }, /eventId must be a positive safe integer/],
    ["source-1", "clone-1", { type: TimeTravelType.EVENT_ID, eventId: -4 }, /eventId must be a positive safe integer/],
    ["source-1", "clone-1", { type: TimeTravelType.EVENT_ID, eventId: 4, stepType: "A" }, /cannot be combined/],
    ["source-1", "clone-1", { type: TimeTravelType.STEP_TYPE, stepType: "A", stepMethod: TimeTravelStepMethod.EXECUTE }, /cannot be combined/],
    ["source-1", "clone-1", { type: TimeTravelType.STEP_EXECUTION_ID, stepExecutionId: "A-1" }, /WAIT_FOR or EXECUTE/],
    ["source-1", "clone-1", { type: "bogus" as TimeTravelType }, /unsupported clone fork type bogus/],
    ["source-1", "clone-1", { timeoutMs: -1_000 }, /non-negative whole number of seconds/],
    ["source-1", "clone-1", { subFlowResetPolicy: "bogus" as SubFlowResetPolicy }, /unsupported SubFlow reset policy/],
    ["source-1", "clone-1", { taskQueue: "dex.other" }, /reserved/],
    ["source-1", "clone-1", { requestId: "" }, /requestId must not be empty/],
    ["same", "same", {}, /must differ from sourceFlowId/],
  ];
  await withFakeService(6, async ({ client, flow, cloneRequests }) => {
    for (const [sourceFlowId, flowId, options, message] of invalid) {
      await assert.rejects(client.cloneForkFlow(flow, sourceFlowId, flowId, options), message);
    }
    await assert.rejects(client.cloneForkFlow(flow, "", "clone-1"));
    assert.equal(cloneRequests.length, 0);
  });
});

test("cloneForkFlow fails before sending to a Server older than protocol 6", async () => {
  await withFakeService(5, async ({ client, flow, cloneRequests }) => {
    await assert.rejects(
      client.cloneForkFlow(flow, "source-1", "clone-1"),
      /cloneForkFlow requires Server protocol version 6 or later \(server-zd 0\.1\.33 or later\); negotiated protocol version is 5/,
    );
    assert.equal(cloneRequests.length, 0);
  });
});

test("cloneForkFlow names the source Flow on not-found and the new Flow on already-started", async () => {
  await withFakeService(6, async (fake) => {
    fake.failure = serviceError(status.NOT_FOUND, ErrorSubStatus.ERROR_SUB_STATUS_FLOW_NOT_EXISTS);
    await assert.rejects(fake.client.cloneForkFlow(fake.flow, "source-1", "clone-1"), (failure: unknown) =>
      failure instanceof FlowNotFoundError && failure.flowId === "source-1" && failure.operation === "cloneForkFlow");
    fake.failure = serviceError(status.ALREADY_EXISTS, ErrorSubStatus.ERROR_SUB_STATUS_FLOW_ALREADY_STARTED);
    await assert.rejects(fake.client.cloneForkFlow(fake.flow, "source-1", "clone-1"), (failure: unknown) =>
      failure instanceof FlowAlreadyStartedError && failure.flowId === "clone-1");
  });
});

test("timeTravel sends the event ID and SubFlow reset policy", async () => {
  await withFakeService(6, async ({ client, resetRequests }) => {
    await client.timeTravel("flow-1", {
      type: TimeTravelType.EVENT_ID,
      eventId: 12,
      subFlowResetPolicy: SubFlowResetPolicy.ALWAYS_REJECT,
    });
    assert.equal(resetRequests[0]!.resetType, FlowResetType.FLOW_RESET_TYPE_EVENT_ID);
    assert.equal(resetRequests[0]!.eventId, 12n);
    assert.equal(resetRequests[0]!.subFlowResetPolicy, ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ALWAYS_REJECT);
    await client.timeTravel("flow-1", { type: TimeTravelType.BEGINNING });
    assert.equal(resetRequests[1]!.subFlowResetPolicy, ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_UNSPECIFIED);
    assert.equal(resetRequests[1]!.eventId, 0n);

    await assert.rejects(client.timeTravel("flow-1", { type: TimeTravelType.CURRENT }), /valid only for cloneForkFlow/);
    await assert.rejects(client.timeTravel("flow-1", { type: TimeTravelType.EVENT_ID }), /eventId must be a positive safe integer/);
    await assert.rejects(
      client.timeTravel("flow-1", { type: TimeTravelType.BEGINNING, subFlowResetPolicy: "bogus" as SubFlowResetPolicy }),
      /unsupported SubFlow reset policy/,
    );
    assert.equal(resetRequests.length, 2);
  });
});

test("timeTravel maps every point and leaves eventId zero for other types", async () => {
  const points: readonly [TimeTravelOptions, FlowResetType, bigint][] = [
    [{ type: TimeTravelType.BEGINNING }, FlowResetType.FLOW_RESET_TYPE_BEGINNING, 0n],
    [{ type: TimeTravelType.HISTORY_EVENT_TIME, historyEventTime: new Date("2026-01-01T00:00:00Z") },
      FlowResetType.FLOW_RESET_TYPE_HISTORY_EVENT_TIME, 0n],
    [{ type: TimeTravelType.STEP_TYPE, stepType: "ChargeOrder" }, FlowResetType.FLOW_RESET_TYPE_STEP_TYPE, 0n],
    [{ type: TimeTravelType.STEP_EXECUTION_ID, stepExecutionId: "step-1", stepMethod: TimeTravelStepMethod.WAIT_FOR },
      FlowResetType.FLOW_RESET_TYPE_STEP_EXECUTION_ID, 0n],
    [{ type: TimeTravelType.EVENT_ID, eventId: 7 }, FlowResetType.FLOW_RESET_TYPE_EVENT_ID, 7n],
  ];
  await withFakeService(6, async ({ client, resetRequests }) => {
    for (const [options, wireType, eventId] of points) {
      await client.timeTravel("flow-1", options);
      const request = resetRequests.at(-1)!;
      assert.equal(request.resetType, wireType);
      assert.equal(request.eventId, eventId);
    }
    assert.equal(resetRequests[1]!.historyEventTime, "2026-01-01T00:00:00.000Z");
    assert.equal(resetRequests[2]!.stepType, "ChargeOrder");
    assert.equal(resetRequests[3]!.stepExecutionId, "step-1");
    assert.equal(resetRequests[3]!.stepMethod, FlowResetStepMethod.FLOW_RESET_STEP_METHOD_WAIT_FOR);
  });
});

test("timeTravel rejects mixed point selectors before sending", async () => {
  const timestamp = new Date("2026-01-01T00:00:00Z");
  const invalid: readonly TimeTravelOptions[] = [
    { type: TimeTravelType.BEGINNING, eventId: 3 },
    { type: TimeTravelType.HISTORY_EVENT_TIME, historyEventTime: timestamp, eventId: 3 },
    { type: TimeTravelType.STEP_TYPE, stepType: "A", eventId: 3 },
    { type: TimeTravelType.STEP_EXECUTION_ID, stepExecutionId: "A-1", stepMethod: TimeTravelStepMethod.EXECUTE, eventId: 3 },
    { type: TimeTravelType.EVENT_ID, eventId: 3, historyEventTime: timestamp },
    { type: TimeTravelType.EVENT_ID, eventId: 3, stepType: "A" },
    { type: TimeTravelType.EVENT_ID, eventId: 3, stepExecutionId: "A-1" },
    { type: TimeTravelType.EVENT_ID, eventId: 3, stepMethod: TimeTravelStepMethod.EXECUTE },
  ];
  await withFakeService(6, async ({ client, resetRequests }) => {
    for (const options of invalid) {
      await assert.rejects(client.timeTravel("flow-1", options), TypeError);
    }
    assert.equal(resetRequests.length, 0);
  });
});

test("SubFlow reset policies map to every wire value", async () => {
  const policies: readonly [SubFlowResetPolicy, ProtoSubFlowResetPolicy][] = [
    [SubFlowResetPolicy.DEFAULT, ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_UNSPECIFIED],
    [SubFlowResetPolicy.ALWAYS_RESTART, ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ALWAYS_RESTART],
    [SubFlowResetPolicy.ALWAYS_FAIL, ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ALWAYS_FAIL],
    [SubFlowResetPolicy.ALWAYS_REJECT, ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ALWAYS_REJECT],
    [SubFlowResetPolicy.ATTACH_ELSE_RESTART, ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_RESTART],
    [SubFlowResetPolicy.ATTACH_ELSE_FAIL, ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_FAIL],
    [SubFlowResetPolicy.ATTACH_ELSE_REJECT, ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_REJECT],
  ];
  await withFakeService(6, async ({ client, flow, cloneRequests }) => {
    for (const [policy, wire] of policies) {
      await client.cloneForkFlow(flow, "source-1", "clone-1", { subFlowResetPolicy: policy });
      assert.equal(cloneRequests.at(-1)!.subFlowResetPolicy, wire);
    }
  });
});

function serviceError(code: status, subStatus: ErrorSubStatus): ServiceError {
  const response = ServiceErrorResponse.encode({
    detail: "clone fork failed",
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
  return Object.assign(new Error("clone fork failed"), { code, details: "clone fork failed", metadata });
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
