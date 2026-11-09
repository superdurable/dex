// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import test from "node:test";

import { Server, ServerCredentials, type sendUnaryData } from "@grpc/grpc-js";

import {
  Action,
  Attribute,
  AttributeMap,
  AttributeMatch,
  Client,
  FlowDefinitionError,
  IndexType,
  InitialAttribute,
  Registry,
  StepList,
  WORK_QUEUE_PERMISSIONS_INDEX_KEY,
  declareRPCMethod,
  gracefulComplete,
  int64Codec,
  rpc,
  stringCodec,
  voidCodec,
  whenAttributeMatches,
  type BlobCache,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "../src/index.js";
import { registeredAttributeIndexes } from "../src/flow.js";
import {
  Context as ProtoContext,
  FlowServiceService,
  IndexType as ProtoIndexType,
  InvokeExecuteMethodRequest,
  InvokeWorkerRPCRequest,
  type FlowServiceServer,
  type ServerInfo,
  type StartFlowRequest,
  type StartFlowResponse,
} from "../src/gen/dex.js";
import type { ValueHydrator } from "../src/value-mapper.js";
import { WorkerDispatcher } from "../src/worker-dispatcher.js";

const caseStatus = new Attribute("case-status", stringCodec);
const priority = new Attribute("priority", int64Codec);
const note = new Attribute("note", stringCodec);

class ApproveStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "ApproveStep";
  }

  public execute(context: Context): StepDecision {
    caseStatus.set(context, "awaiting-manager");
    return gracefulComplete(undefined);
  }
}

class NoteStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "NoteStep";
  }

  public execute(context: Context): StepDecision {
    note.set(context, "unrelated");
    return gracefulComplete(undefined);
  }
}

class RefundActionFlow implements Flow<void> {
  private readonly approve = new ApproveStep();
  private readonly noteStep = new NoteStep();

  public getFlowType(): string {
    return "RefundActionFlow";
  }

  public getSteps() {
    return StepList.startStep(this.approve).otherSteps(this.noteStep);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [caseStatus, priority, note] };
  }

  @rpc({
    lockAttributes: [caseStatus.lock()],
    action: new Action({
      label: "Approve refund",
      requiredPermission: "refund.approve",
      condition: whenAttributeMatches(
        caseStatus,
        AttributeMatch.equalTo("awaiting-manager"),
        AttributeMatch.equalTo("escalated"),
      ),
    }),
  })
  public approveRefund(context: Context): RPCResult<void> {
    caseStatus.set(context, "approved");
    return { output: undefined };
  }

  @rpc({
    action: new Action({
      label: "Escalate",
      requiredPermission: "refund.escalate",
      condition: whenAttributeMatches(priority, AttributeMatch.equalTo(3n)),
    }),
  })
  public escalateRefund(_context: Context): RPCResult<void> {
    return { output: undefined };
  }

  @rpc({
    action: new Action({
      label: "Review",
      requiredPermission: "refund.approve",
      condition: whenAttributeMatches(caseStatus, AttributeMatch.equalTo("approved")),
    }),
  })
  public reviewRefund(_context: Context): RPCResult<void> {
    return { output: undefined };
  }

  @rpc({ action: new Action({ label: "Cancel", requiredPermission: "refund.cancel" }) })
  public cancelRefund(_context: Context): RPCResult<void> {
    return { output: undefined };
  }

  @rpc()
  public writeNote(context: Context): RPCResult<void> {
    note.set(context, "hello");
    return { output: undefined };
  }
}

test("Registry accepts Actions with and without a condition and registers the permission index", () => {
  const registry = new Registry([new RefundActionFlow()]);
  assert.equal(
    registeredAttributeIndexes(registry).get(WORK_QUEUE_PERMISSIONS_INDEX_KEY),
    ProtoIndexType.INDEX_TYPE_KEYWORD_ARRAY,
  );
  assert.equal(WORK_QUEUE_PERMISSIONS_INDEX_KEY, "DexWorkQueuePermissions");
});

test("Registry without Actions does not register the permission index", () => {
  class PlainFlow implements Flow<void> {
    public getFlowType(): string {
      return "PlainFlow";
    }

    public getSteps() {
      return StepList.empty();
    }

    @rpc()
    public getStatus(_context: Context): RPCResult<void> {
      return { output: undefined };
    }
  }
  const registry = new Registry([new PlainFlow()]);
  assert.equal(registeredAttributeIndexes(registry).has(WORK_QUEUE_PERMISSIONS_INDEX_KEY), false);
});

test("Registry rejects invalid Actions", () => {
  const hardDelete = new Attribute("secret", stringCodec).hardDeleteOnNull();
  const items = new AttributeMap("items", stringCodec);
  const unregistered = new Attribute("unregistered", stringCodec);
  const lookalike = new Attribute("case-status", stringCodec, { type: IndexType.KEYWORD });
  const cases: ReadonlyArray<readonly [string, () => Action, RegExp]> = [
    ["blank label", () => new Action({ label: "  ", requiredPermission: "refund.approve" }), /label must not be empty/],
    ["bad permission", () => new Action({ label: "Go", requiredPermission: "Refund.Approve" }), /permission "Refund.Approve" must use/],
    ["empty permission", () => new Action({ label: "Go", requiredPermission: "" }), /permission "" must use/],
    [
      "non-equal match",
      () => new Action({
        label: "Go",
        requiredPermission: "refund.approve",
        condition: whenAttributeMatches(caseStatus, AttributeMatch.notEqualTo("done")),
      }),
      /supports only AttributeMatch.equalTo/,
    ],
    [
      "no match",
      () => new Action({ label: "Go", requiredPermission: "refund.approve", condition: whenAttributeMatches(caseStatus) }),
      /requires at least one match/,
    ],
    [
      "unregistered Attribute",
      () => new Action({
        label: "Go",
        requiredPermission: "refund.approve",
        condition: whenAttributeMatches(unregistered, AttributeMatch.equalTo("x")),
      }),
      /"unregistered" is not declared by Flow "InvalidActionFlow"/,
    ],
    [
      "hard-delete Attribute",
      () => new Action({
        label: "Go",
        requiredPermission: "refund.approve",
        condition: whenAttributeMatches(hardDelete, AttributeMatch.equalTo("x")),
      }),
      /uses hardDeleteOnNull/,
    ],
    [
      "mismatched definition",
      () => new Action({
        label: "Go",
        requiredPermission: "refund.approve",
        condition: whenAttributeMatches(lookalike, AttributeMatch.equalTo("x")),
      }),
      /does not match its registered definition/,
    ],
  ];
  for (const [name, action, expected] of cases) {
    class InvalidActionFlow implements Flow<void> {
      public getFlowType(): string {
        return "InvalidActionFlow";
      }

      public getSteps() {
        return StepList.empty();
      }

      public getPersistenceSchema(): PersistenceSchema {
        return { attributes: [caseStatus, hardDelete, items] };
      }

      public decideRefund(_context: Context): RPCResult<void> {
        return { output: undefined };
      }
    }
    declareRPCMethod(InvalidActionFlow, "decideRefund", { action: action() });
    assert.throws(
      () => new Registry([new InvalidActionFlow()]),
      (failure: Error) =>
        failure instanceof FlowDefinitionError &&
        /RPC decideRefund Action/.test(failure.message) &&
        expected.test(failure.message),
      name,
    );
  }
});

test("Registry rejects an AttributeMap condition", () => {
  const items = new AttributeMap("items", stringCodec);
  class MapConditionFlow implements Flow<void> {
    public getFlowType(): string {
      return "MapConditionFlow";
    }

    public getSteps() {
      return StepList.empty();
    }

    public getPersistenceSchema(): PersistenceSchema {
      return { attributes: [items] };
    }

    public decideRefund(_context: Context): RPCResult<void> {
      return { output: undefined };
    }
  }
  assert.throws(
    () => whenAttributeMatches(items as unknown as Attribute<string>, AttributeMatch.equalTo("x")),
    /requires a scalar Attribute definition/,
  );
  const condition = whenAttributeMatches(new Attribute("items", stringCodec), AttributeMatch.equalTo("x"));
  declareRPCMethod(MapConditionFlow, "decideRefund", {
    action: new Action({ label: "Go", requiredPermission: "refund.approve", condition }),
  });
  assert.throws(() => new Registry([new MapConditionFlow()]), /"items" is not declared by Flow "MapConditionFlow"/);
});

test("Worker sends every conditioned Action mapping when an RPC writes a condition Attribute", async () => {
  const flow = new RefundActionFlow();
  const dispatcher = new WorkerDispatcher(new Registry([flow]), passthroughHydrator(), () => 4);
  const response = await dispatcher.invokeRPC(rpcRequest(flow, "approveRefund"));
  assert.deepEqual(response.actionPermissionMappings, {
    mappings: [
      {
        attributeKey: "case-status",
        equalValues: [
          { kind: { $case: "stringValue", value: "awaiting-manager" } },
          { kind: { $case: "stringValue", value: "escalated" } },
        ],
        requiredPermission: "refund.approve",
      },
      {
        attributeKey: "priority",
        equalValues: [{ kind: { $case: "intValue", value: 3n } }],
        requiredPermission: "refund.escalate",
      },
      {
        attributeKey: "case-status",
        equalValues: [{ kind: { $case: "stringValue", value: "approved" } }],
        requiredPermission: "refund.approve",
      },
    ],
  });
});

test("Worker omits mappings when no condition Attribute is written", async () => {
  const flow = new RefundActionFlow();
  const dispatcher = new WorkerDispatcher(new Registry([flow]), passthroughHydrator(), () => 4);
  assert.equal((await dispatcher.invokeRPC(rpcRequest(flow, "writeNote"))).actionPermissionMappings, undefined);
  assert.equal((await dispatcher.invokeRPC(rpcRequest(flow, "cancelRefund"))).actionPermissionMappings, undefined);
  const execute = await dispatcher.invokeExecute(executeRequest(flow, "NoteStep"));
  assert.equal(execute.actionPermissionMappings, undefined);
});

test("Worker sends mappings from an Execute that writes a condition Attribute", async () => {
  const flow = new RefundActionFlow();
  const dispatcher = new WorkerDispatcher(new Registry([flow]), passthroughHydrator(), () => 4);
  const response = await dispatcher.invokeExecute(executeRequest(flow, "ApproveStep"));
  assert.equal(response.actionPermissionMappings?.mappings.length, 3);
  assert.deepEqual(
    response.actionPermissionMappings?.mappings.map((mapping) => mapping.requiredPermission),
    ["refund.approve", "refund.escalate", "refund.approve"],
  );
});

test("startFlow records the permissions the initial Attributes make available", async () => {
  const flow = new RefundActionFlow();
  const fake = await startFakeFlowService();
  const client = new Client(new Registry([flow]), new MemoryBlobCache(), { serverAddress: fake.address });
  try {
    await client.startFlow(flow, "matching", undefined, {
      attributes: [InitialAttribute.of(caseStatus, "escalated"), InitialAttribute.of(priority, 3n)],
    });
    const writes = fake.startRequests.at(-1)?.flowStartOptions?.attributes ?? [];
    const projection = writes.find((write) => write.key === WORK_QUEUE_PERMISSIONS_INDEX_KEY);
    assert.ok(projection !== undefined);
    assert.equal(projection.value?.kind?.$case, "objValue");
    const encoded = projection.value?.kind?.$case === "objValue" ? projection.value.kind.value : undefined;
    assert.equal(encoded?.encoding, "json");
    assert.equal(new TextDecoder().decode(encoded?.payload), '["refund.approve","refund.escalate"]');
    assert.deepEqual(projection.indexConfig, {
      enable: true,
      type: ProtoIndexType.INDEX_TYPE_KEYWORD_ARRAY,
      indexKey: "DexWorkQueuePermissions",
    });

    await client.startFlow(flow, "deduplicated", undefined, {
      attributes: [InitialAttribute.of(caseStatus, "approved")],
    });
    const deduplicated = (fake.startRequests.at(-1)?.flowStartOptions?.attributes ?? [])
      .find((write) => write.key === WORK_QUEUE_PERMISSIONS_INDEX_KEY);
    const payload = deduplicated?.value?.kind?.$case === "objValue" ? deduplicated.value.kind.value.payload : undefined;
    assert.equal(new TextDecoder().decode(payload), '["refund.approve"]');

    for (const attributes of [[InitialAttribute.of(caseStatus, "new")], [InitialAttribute.of(priority, 2n)], []]) {
      await client.startFlow(flow, "not-matching", undefined, { attributes });
      const unmatched = fake.startRequests.at(-1)?.flowStartOptions?.attributes ?? [];
      assert.equal(unmatched.some((write) => write.key === WORK_QUEUE_PERMISSIONS_INDEX_KEY), false);
    }
  } finally {
    await client.close();
    await fake.close();
  }
});

function rpcRequest(flow: { getFlowType(): string }, rpcName: string): InvokeWorkerRPCRequest {
  return InvokeWorkerRPCRequest.create({
    context: ProtoContext.create({ flowId: "flow-1" }),
    flowType: flow.getFlowType(),
    rpcName,
    attributes: [],
  });
}

function executeRequest(flow: { getFlowType(): string }, stepType: string): InvokeExecuteMethodRequest {
  return InvokeExecuteMethodRequest.create({
    context: ProtoContext.create({ flowId: "flow-1", stepExecutionId: "step-1" }),
    flowType: flow.getFlowType(),
    stepType,
    stepInput: { kind: { $case: "nullValue", value: 0 } },
    attributes: [],
  });
}

function passthroughHydrator(): ValueHydrator {
  return {
    hydrateAll: async (_flowId: string, values: readonly unknown[]) => values,
  } as unknown as ValueHydrator;
}

interface FakeFlowService {
  readonly address: string;
  readonly startRequests: StartFlowRequest[];
  close(): Promise<void>;
}

async function startFakeFlowService(): Promise<FakeFlowService> {
  const startRequests: StartFlowRequest[] = [];
  const server = new Server();
  server.addService(FlowServiceService, {
    getServerInfo(_call, callback: sendUnaryData<ServerInfo>) {
      callback(null, {
        serverVersion: "test",
        minimumSupportedProtocolVersion: 1,
        currentProtocolVersion: 4,
        serverEngine: 0,
      });
    },
    startFlow(call, callback: sendUnaryData<StartFlowResponse>) {
      startRequests.push(call.request as StartFlowRequest);
      callback(null, { runId: "run-1" });
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
  return {
    address: `127.0.0.1:${port}`,
    startRequests,
    close: () => new Promise((resolve) => server.tryShutdown(() => resolve())),
  };
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
