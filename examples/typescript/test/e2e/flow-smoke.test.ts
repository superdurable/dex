/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { Client, Registry, StepExecutionId, Worker, rpc, type Context } from "@superdurable/dex";

import { loadEnv } from "../../src/config/env.js";
import type { SampleServer } from "../../src/main.js";
import { attributeFlow } from "../../src/primitives/attribute/attribute-flow.js";
import { MockMessageModel } from "../../src/products/customer-refund/message-model.js";
import {
  AWAITING_MANAGER,
  CustomerRefundFlow,
  caseStatus,
  type DecisionReason,
} from "../../src/products/customer-refund/refund-flow.js";
import { REFUND_SCENARIOS, openRefundBlobCache } from "../../src/products/customer-refund/refund-settings.js";
import {
  assertFlowSmokeNoUnexpectedFailures,
  assertFlowSmokeStartStep,
  defaultFlags,
  stepStartMayFailFlags,
  noStartStepFlags,
  type FlowSmokeContext,
  type FlowSmokeEntry,
  newFlowId,
  triggerGet,
  triggerPost,
} from "./flow-smoke-helper.js";
import {
  assertRecordedRefund,
  startSharedServerTestEnvironment,
  type SharedServerTestEnvironment,
} from "./shared-server-test-environment.js";

let environment: SharedServerTestEnvironment;
let server: SampleServer;
let context: FlowSmokeContext;

test.before(async () => {
  process.env.DEX_EXAMPLES_HTTP_ADDRESS =
    process.env.DEX_SMOKE_HTTP_ADDRESS ?? `127.0.0.1:${await availablePort()}`;
  environment = await startSharedServerTestEnvironment();
  server = environment.server;
  const httpAddress = loadEnv().httpAddress;
  context = {
    baseUrl: `http://${httpAddress}`,
    newFlowId,
  };
});

test.after(async () => {
  await environment?.close();
});

function flowSmokeCatalog(): FlowSmokeEntry[] {
  return [
    { name: "products/engagement", trigger: () => triggerGet(context, "/products/engagement/start"), flags: defaultFlags() },
    {
      name: "products/microservices",
      trigger: () =>
        triggerGet(context, "/products/microservices/start", {
          workflowId: newFlowId("microservices"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "products/money-transfer",
      trigger: () =>
        triggerGet(context, "/products/money-transfer/start", {
          amount: 100,
          fromAccount: "from-smoke",
          toAccount: "to-smoke",
          notes: "smoke",
        }),
      flags: defaultFlags(),
    },
    {
      name: "products/order-processing",
      trigger: () => triggerGet(context, "/products/order-processing/start"),
      flags: defaultFlags(),
    },
    {
      name: "products/customer-refund/manager-approval",
      trigger: () => triggerGet(context, "/products/customer-refund/manager-approval/start"),
      flags: defaultFlags(),
    },
    {
      name: "products/subscription",
      trigger: () => triggerGet(context, "/products/subscription/start"),
      flags: defaultFlags(),
    },
    {
      name: "products/signup",
      trigger: async () => {
        const username = newFlowId("signup");
        const result = await triggerGet(context, "/products/signup/submit", {
          username,
          email: `${username}@example.com`,
        });
        return {
          flowId: result.flowId || username,
          runId: result.runId,
        };
      },
      flags: defaultFlags(),
    },
    {
      name: "products/job-post",
      trigger: () =>
        triggerGet(context, "/products/job-post/create", {
          title: "Smoke Test Job",
          description: "Smoke test description",
        }),
      flags: noStartStepFlags(),
    },
    {
      name: "patterns/polling",
      trigger: () =>
        triggerGet(context, "/patterns/polling/start", {
          workflowId: newFlowId("pattern-polling"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/interruptible",
      trigger: () =>
        triggerGet(context, "/patterns/interruptible/start", {
          workflowId: newFlowId("interruptible"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/reminders",
      trigger: () => triggerGet(context, "/patterns/reminders/start"),
      flags: defaultFlags(),
    },
    {
      name: "patterns/entity-store",
      trigger: async () => {
        const userId = newFlowId("entity-store");
        const result = await triggerPost(context, "/patterns/entity-store/profile", {
          userId,
          displayName: "Smoke Tester",
          email: `${userId}@example.com`,
          marketingOptIn: true,
          credits: 120,
          weight: 59.5,
          lastLoggedInTime: "2026-08-11T15:30:00Z",
          metadata: { source: "smoke", tags: ["example"] },
        });
        return { flowId: result.flowId || userId, runId: result.runId };
      },
      flags: noStartStepFlags(),
    },
    {
      name: "patterns/sequentially-chunked-attribute-map",
      trigger: () =>
        triggerPost(
          context,
          "/patterns/sequentially-chunked-attribute-map/start",
          undefined,
        ),
      flags: noStartStepFlags(),
    },
    {
      name: "patterns/hash-partitioned-attribute-map",
      trigger: () =>
        triggerPost(
          context,
          "/patterns/hash-partitioned-attribute-map/start",
          undefined,
        ),
      flags: noStartStepFlags(),
    },
    {
      name: "patterns/manual-recovery",
      trigger: () =>
        triggerGet(context, "/patterns/manual-recovery/start", {
          workflowId: newFlowId("manual-recovery"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/inactiveness-tracker-timer",
      trigger: () =>
        triggerGet(context, "/patterns/inactiveness-tracker-timer/start", {
          workflowId: newFlowId("inactiveness-tracker-timer"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/parallel/static",
      trigger: () =>
        triggerGet(context, "/patterns/parallel/start/static", {
          workflowId: newFlowId("parallel-static"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/parallel/dynamic",
      trigger: () =>
        triggerGet(context, "/patterns/parallel/start/dynamic", {
          workflowId: newFlowId("parallel-dynamic"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/parallel/await",
      trigger: () =>
        triggerGet(context, "/patterns/parallel/start/await", {
          workflowId: newFlowId("parallel-await"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/parallel/first-win",
      trigger: () =>
        triggerGet(context, "/patterns/parallel/start/first-win", {
          workflowId: newFlowId("parallel-first-win"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/recovery",
      trigger: () =>
        triggerGet(context, "/patterns/recovery/start", {
          workflowId: newFlowId("recovery"),
          itemName: "smoke-item",
          quantity: 2,
        }),
      flags: stepStartMayFailFlags(),
    },
    parallelSubFlowsSmokeEntry("basic"),
    parallelSubFlowsSmokeEntry("wait-for-half"),
    parallelSubFlowsSmokeEntry("long-lived-parent"),
    parallelSubFlowsSmokeEntry("short-lived-parent"),
    {
      name: "patterns/drain-channels/internal",
      trigger: () =>
        triggerGet(context, "/patterns/drain-channels/internal/start", {
          workflowId: newFlowId("drain-internal"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/drain-channels/external-publishing",
      trigger: () =>
        triggerGet(context, "/patterns/drain-channels/external-publishing/start-or-publish", {
          workflowId: newFlowId("drain-external"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/wait-for-step-completion",
      trigger: () =>
        triggerGet(context, "/patterns/wait-for-step-completion/start", {
          workflowId: newFlowId("wait-for-state"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/timeout",
      trigger: () =>
        triggerGet(context, "/patterns/timeout/start", {
          workflowId: newFlowId("timeout"),
          successfulWorkflow: true,
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/step",
      trigger: () =>
        triggerGet(context, "/primitives/step/start", {
          workflowId: newFlowId("primitive-step"),
          inputNum: 1,
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/step/retry",
      trigger: () =>
        triggerGet(context, "/primitives/step/retry/start", {
          workflowId: newFlowId("primitive-step-retry"),
          readyAfterAttempt: 2,
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/step/custom-retry",
      trigger: () =>
        triggerGet(context, "/primitives/step/custom-retry/start", {
          workflowId: newFlowId("primitive-step-custom-retry"),
          readyAfterAttempt: 1,
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/step/durability",
      trigger: () =>
        triggerGet(context, "/primitives/step/durability/start", {
          workflowId: newFlowId("primitive-step-durability"),
          mode: "sync",
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/step/heartbeat",
      trigger: () =>
        triggerGet(context, "/primitives/step/heartbeat/start", {
          workflowId: newFlowId("primitive-step-heartbeat"),
          batches: 0,
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/step/options-override",
      trigger: () =>
        triggerGet(context, "/primitives/step/options-override/start", {
          workflowId: newFlowId("primitive-step-options-override"),
          input: "smoke",
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/step/step-decision",
      trigger: () =>
        triggerGet(context, "/primitives/step/step-decision/start", {
          workflowId: newFlowId("primitive-step-decision"),
          mode: "graceful",
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/step/wait-types",
      trigger: () =>
        triggerGet(context, "/primitives/step/wait-types/start", {
          workflowId: newFlowId("primitive-step-wait-types"),
          mode: "any",
          timeoutSeconds: 1,
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/attribute",
      trigger: () =>
        triggerGet(context, "/primitives/attribute/start", {
          workflowId: newFlowId("primitive-attribute"),
          message: "smoke",
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/channel",
      trigger: () =>
        triggerGet(context, "/primitives/channel/start", {
          workflowId: newFlowId("primitive-channel"),
          inputNum: 1,
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/stream",
      trigger: () =>
        triggerGet(context, "/primitives/stream/start", {
          workflowId: newFlowId("primitive-stream"),
          input: "smoke",
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/timer",
      trigger: () =>
        triggerGet(context, "/primitives/timer/start", {
          workflowId: newFlowId("primitive-timer"),
          seconds: 1,
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/rpc",
      trigger: () =>
        triggerGet(context, "/primitives/rpc/start", {
          workflowId: newFlowId("primitive-rpc"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/rpc-canceling-steps",
      trigger: () =>
        triggerGet(context, "/primitives/rpc-canceling-steps/start", {
          workflowId: newFlowId("primitive-rpc-canceling-steps"),
          reportName: "smoke",
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/subflow",
      trigger: () =>
        triggerGet(context, "/primitives/subflow/start", {
          workflowId: newFlowId("primitive-subflow"),
          inputNum: 1,
        }),
      flags: defaultFlags(),
    },
    {
      name: "primitives/client-apis",
      trigger: () =>
        triggerGet(context, "/primitives/client-apis/start", {
          workflowId: newFlowId("primitive-client-apis"),
          keyword: "smoke",
        }),
      flags: defaultFlags(),
    },
  ];
}

function parallelSubFlowsSmokeEntry(kind: string): FlowSmokeEntry {
  return {
    name: `patterns/parallel-subflows/${kind}`,
    trigger: () =>
      triggerGet(context, `/patterns/parallel-subflows/start/${kind}`, {
        workflowId: newFlowId(`parallel-subflows-${kind}`),
      }),
    flags: defaultFlags(),
  };
}

test("flow smoke catalog size", () => {
  const catalog = flowSmokeCatalog();
  assert.ok(catalog.length > 0, "flow smoke catalog is empty");
});

test("flow smoke all registered flows via controller", async (t) => {
  for (const entry of flowSmokeCatalog()) {
    await t.test(entry.name, async () => {
      try {
        const result = await entry.trigger(context);
        assert.ok(result.flowId, `${entry.name}: controller response did not include flowID`);
        await assertFlowSmokeStartStep(entry, result.flowId, result.runId);
        if (entry.name === "patterns/interruptible") {
          await triggerGet(context, "/patterns/interruptible/cancel", {
            workflowId: result.flowId,
          });
        }
        await assertFlowSmokeNoUnexpectedFailures(entry, result.flowId, result.runId);
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        throw new Error(`${entry.name}: ${message}`, { cause: error });
      }
    });
  }
});

test("attribute read returns committed values of a closed run", async () => {
  const flowId = newFlowId("primitive-attribute-read");
  await server.client.startFlow(attributeFlow, flowId, "read");
  const result = await server.client.waitForFlow(flowId, 30_000);
  assert.equal(result.status, "completed");

  const response = await fetch(attributeReadUrl({ workflowId: flowId }));
  const body = await response.text();
  assert.equal(response.status, 200, body);
  assert.deepEqual(JSON.parse(body), {
    status: "completed",
    paymentProgress: "authorized",
    email: null,
  });
});

test("attribute read returns not found for a missing Flow", async () => {
  const flowId = newFlowId("primitive-attribute-read-missing");
  const response = await fetch(attributeReadUrl({ workflowId: flowId }));
  const body = await response.text();
  assert.equal(response.status, 404, body);
  const errorBody = JSON.parse(body) as Record<string, string>;
  assert.deepEqual(Object.keys(errorBody), ["error"]);
  assert.ok(errorBody.error?.includes(flowId), body);
});

test("attribute read requires workflowId", async () => {
  const response = await fetch(attributeReadUrl({}));
  const body = await response.text();
  assert.equal(response.status, 400, body);
  assert.deepEqual(JSON.parse(body), { error: "workflowId is required" });
});

test("customer refund routes start, approve, and describe a refund", async () => {
  const workflowId = newFlowId("customer-refund");
  const start = await refundRoute("start", { workflowId: "", flowId: workflowId, scenario: "" });
  assert.equal(start.status, 200, start.text);
  assert.equal(typeof start.body.runID, "string", start.text);
  assert.ok(start.body.runID, start.text);
  assert.deepEqual(start.body, { flowID: workflowId, runID: start.body.runID });

  const awaiting = await refundRoute("describe", { workflowId: "", flowId: workflowId });
  assert.equal(awaiting.status, 200, awaiting.text);
  assert.deepEqual(awaiting.body, {
    flowID: workflowId, status: "running", caseStatus: AWAITING_MANAGER, refundId: null,
  });

  const approve = await refundRoute("decide", {
    workflowId: "", flowId: workflowId, decision: "approve", reason: "Outage confirmed",
  });
  assert.equal(approve.status, 200, approve.text);
  assert.deepEqual(approve.body, { flowID: workflowId, decision: "approve" });
  const result = await server.client.waitForFlow(workflowId, 30_000);
  assert.equal(result.status, "completed", result.errorMessage);

  const closed = await refundRoute("describe", { workflowId });
  assert.equal(closed.status, 200, closed.text);
  assert.deepEqual(closed.body, {
    flowID: workflowId,
    status: "completed",
    caseStatus: "closed-refunded",
    refundId: assertRecordedRefund(environment, workflowId),
  });
});

test("customer refund routes reject and describe a refund", async () => {
  const workflowId = newFlowId("customer-refund-reject");
  const start = await refundRoute("start", { workflowId });
  assert.equal(start.status, 200, start.text);

  const reject = await refundRoute("decide", { workflowId, decision: "reject", reason: "Outage disputed" });
  assert.equal(reject.status, 200, reject.text);
  assert.deepEqual(reject.body, { flowID: workflowId, decision: "reject" });
  const result = await server.client.waitForFlow(workflowId, 30_000);
  assert.equal(result.status, "completed", result.errorMessage);

  const closed = await refundRoute("describe", { workflowId });
  assert.equal(closed.status, 200, closed.text);
  assert.deepEqual(closed.body, {
    flowID: workflowId, status: "completed", caseStatus: "closed-denied", refundId: null,
  });
  assert.ok(!environment.billing.listRefunds().some((refund) => refund.idempotencyKey === `refund-${workflowId}`));
});

test("customer refund start mints a Flow ID and describes a completed small refund", async () => {
  const start = await refundRoute("start", { flowId: "", scenario: "small" });
  assert.equal(start.status, 200, start.text);
  const flowId = String(start.body.flowID);
  assert.match(flowId, /^customer-refund-\d+$/);
  const result = await server.client.waitForFlow(flowId, 30_000);
  assert.equal(result.status, "completed", result.errorMessage);
  const closed = await refundRoute("describe", { flowId });
  assert.equal(closed.status, 200, closed.text);
  assert.deepEqual(closed.body, {
    flowID: flowId,
    status: "completed",
    caseStatus: "closed-refunded",
    refundId: assertRecordedRefund(environment, flowId),
  });
});

test("customer refund start and describe work for a policy denial", async () => {
  const flowId = newFlowId("customer-refund-deny");
  const start = await refundRoute("start", { flowId, scenario: "deny" });
  assert.equal(start.status, 200, start.text);
  const result = await server.client.waitForFlow(flowId, 30_000);
  assert.equal(result.status, "completed", result.errorMessage);
  const closed = await refundRoute("describe", { flowId });
  assert.equal(closed.status, 200, closed.text);
  assert.deepEqual(closed.body, {
    flowID: flowId, status: "completed", caseStatus: "closed-denied", refundId: null,
  });
});

test("customer refund decide refuses a blank reason, an unknown decision, and a closed run", async () => {
  const workflowId = newFlowId("customer-refund-small");
  const start = await refundRoute("start", { workflowId, scenario: "small" });
  assert.equal(start.status, 200, start.text);

  const blank = await refundRoute("decide", { workflowId, decision: "approve", reason: "  " });
  assert.equal(blank.status, 400, blank.text);
  assert.deepEqual(blank.body, { error: "a reason is required" });
  const unknown = await refundRoute("decide", { workflowId, decision: "maybe", reason: "Outage confirmed" });
  assertRefundError(unknown, 400);

  const result = await server.client.waitForFlow(workflowId, 30_000);
  assert.equal(result.status, "completed", result.errorMessage);
  const closed = await refundRoute("decide", { workflowId, decision: "approve", reason: "Outage confirmed" });
  assertRefundError(closed, 404);
  assertRecordedRefund(environment, workflowId);
});

test("customer refund concurrent decisions have exactly one winner", async () => {
  const workflowId = newFlowId("customer-refund-retry");
  const start = await refundRoute("start", { workflowId, scenario: "fail-then-succeed" });
  assert.equal(start.status, 200, start.text);

  const decision = { workflowId, decision: "approve", reason: "Outage confirmed" };
  const decisions = await Promise.all([refundRoute("decide", decision), refundRoute("decide", decision)]);
  const [winner, loser] = decisions.sort((first, second) => first.status - second.status);
  assert.equal(winner!.status, 200, winner!.text);
  assert.deepEqual(winner!.body, { flowID: workflowId, decision: "approve" });
  assertRefundError(loser!, 409);

  const running = await refundRoute("describe", { workflowId });
  assert.equal(running.status, 200, running.text);
  assert.equal(running.body.status, "running");
  const repeated = await refundRoute("decide", decision);
  assertRefundError(repeated, 409);
  assert.match(String(repeated.body.error), /not waiting for a manager/);

  const result = await server.client.waitForFlow(workflowId, 30_000);
  assert.equal(result.status, "completed", result.errorMessage);
  assertRecordedRefund(environment, workflowId);
});

function createRefundLockGate(): { wait: Promise<void>; signal: () => void } {
  let signal!: () => void;
  const wait = new Promise<void>((resolve) => { signal = resolve; });
  return { wait, signal };
}

class RefundRpcTestFlow extends CustomerRefundFlow {
  public readonly lockAcquired = createRefundLockGate();
  public readonly releaseLock = createRefundLockGate();

  @rpc({ lockAttributes: [caseStatus.lock()], timeoutMs: 10_000 })
  public async holdRefundStatusLock(_context: Context): Promise<void> {
    this.lockAcquired.signal();
    await this.releaseLock.wait;
  }

  @rpc({ lockAttributes: [caseStatus.lock()] })
  public override rejectRefund(_context: Context, _input: DecisionReason): void {
    throw new Error("test refund Worker failure");
  }
}

test("customer refund decides distinguish a held lock from a Worker failure", async () => {
  const directory = await mkdtemp(join(tmpdir(), "dex-refund-rpc-"));
  const cache = openRefundBlobCache(directory);
  const flow = new RefundRpcTestFlow(environment.billing, environment.email, new MockMessageModel());
  const registry = new Registry([flow]);
  const serverAddress = loadEnv().serverAddress;
  const taskQueue = newFlowId("refund-rpc");
  const worker = new Worker(registry, cache, { serverAddress, pull: { taskQueue } });
  const client = new Client(registry, cache, { serverAddress });
  try {
    await worker.start();
    await worker.waitPullRegistered({ timeoutMs: 10_000 });
    const workflowId = newFlowId("refund-rpc");
    await client.startFlow(flow, workflowId, REFUND_SCENARIOS.normal, { taskQueue });
    await client.waitForStepCompletion(workflowId, StepExecutionId.of("CheckPolicy"), { requestTimeoutMs: 10_000 });

    const decision = { workflowId, decision: "approve", reason: "Outage confirmed" };
    const heldLock = client.invokeRPC(flow.holdRefundStatusLock, workflowId);
    try {
      await Promise.race([flow.lockAcquired.wait, heldLock.then(() => assert.fail("refund lock was released early"))]);
      const conflict = await refundRoute("decide", decision);
      assertRefundError(conflict, 409);
      assert.deepEqual(conflict.body, { error: `another decision on refund ${workflowId} is being processed` });
    } finally {
      flow.releaseLock.signal();
      await heldLock;
    }

    const failure = await refundRoute("decide", { ...decision, decision: "reject" });
    assertRefundError(failure, 500);
    assert.match(String(failure.body.error), /test refund Worker failure/);
    const approved = await refundRoute("decide", decision);
    assert.equal(approved.status, 200, approved.text);
    const result = await client.waitForFlow(workflowId, 30_000);
    assert.equal(result.status, "completed", result.errorMessage);
    assertRecordedRefund(environment, workflowId);
  } finally {
    try {
      await Promise.all([worker.close(), client.close()]);
    } finally {
      cache.close();
      await rm(directory, { recursive: true, force: true });
    }
  }
});

test("customer refund start refuses an unknown scenario and a duplicate Flow ID", async () => {
  const start = await refundRoute("start", { scenario: "unknown" });
  assertRefundError(start, 400);

  const workflowId = newFlowId("customer-refund-duplicate");
  const first = await refundRoute("start", { workflowId });
  assert.equal(first.status, 200, first.text);
  assertRefundError(await refundRoute("start", { workflowId }), 409);
});

test("customer refund describe and decide require a Flow ID and refuse an unknown Flow", async () => {
  const decision = { decision: "approve", reason: "Outage confirmed" };
  assertRefundError(await refundRoute("describe", {}), 400);
  assertRefundError(await refundRoute("decide", decision), 400);

  const workflowId = newFlowId("customer-refund-missing");
  assertRefundError(await refundRoute("describe", { workflowId }), 404);
  assertRefundError(await refundRoute("decide", { workflowId, ...decision }), 404);
});

test("customer refund describe and decide refuse a different Flow type", async () => {
  const workflowId = newFlowId("customer-refund-other-flow-type");
  await server.client.startFlow(attributeFlow, workflowId, "read", { taskQueue: newFlowId("other-flow") });
  try {
    assertRefundError(await refundRoute("describe", { workflowId }), 404);
    assertRefundError(await refundRoute("decide", { workflowId, decision: "approve", reason: "Outage confirmed" }), 404);
  } finally {
    await server.client.stopFlow(workflowId);
  }
});

interface RefundRouteResponse {
  readonly status: number;
  readonly text: string;
  readonly body: Record<string, unknown>;
}

function assertRefundError(response: RefundRouteResponse, status: number): void {
  assert.equal(response.status, status, response.text);
  assert.deepEqual(Object.keys(response.body), ["error"]);
  assert.equal(typeof response.body.error, "string");
}

async function refundRoute(
  action: "start" | "decide" | "describe",
  query: Record<string, string>,
): Promise<RefundRouteResponse> {
  const url = `${context.baseUrl}/products/customer-refund/manager-approval/${action}?${new URLSearchParams(query)}`;
  const response = await fetch(url);
  const text = await response.text();
  return { status: response.status, text, body: JSON.parse(text) as Record<string, unknown> };
}

function attributeReadUrl(query: Record<string, string>): string {
  return `${context.baseUrl}/primitives/attribute/read?${new URLSearchParams(query)}`;
}

function availablePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (address === null || typeof address === "string") {
        server.close();
        reject(new Error("failed to allocate a smoke test port"));
        return;
      }
      server.close((error) => {
        if (error !== undefined) {
          reject(error);
          return;
        }
        resolve(address.port);
      });
    });
  });
}
