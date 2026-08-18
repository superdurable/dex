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
import test from "node:test";

import { loadEnv } from "../../src/config/env.js";
import { startSampleServer, type SampleServer } from "../../src/main.js";
import {
  assertFlowSmokeNoUnexpectedFailures,
  assertFlowSmokeStartStep,
  defaultFlags,
  stepStartMayFailFlags,
  employerOptInFlowId,
  type FlowSmokeContext,
  type FlowSmokeEntry,
  newFlowId,
  shortlistFlowId,
  triggerGet,
  triggerPost,
} from "./flow-smoke-helper.js";

let server: SampleServer;
let context: FlowSmokeContext;

test.before(async () => {
  process.env.DEX_EXAMPLES_HTTP_ADDRESS =
    process.env.DEX_SMOKE_HTTP_ADDRESS ?? "127.0.0.1:0";
  process.env.DEX_WORKER_BIND_ADDRESS =
    process.env.DEX_SMOKE_WORKER_BIND_ADDRESS ?? "127.0.0.1:0";
  server = await startSampleServer();
  const httpAddress = loadEnv().httpAddress;
  context = {
    baseUrl: `http://${httpAddress}`,
    newFlowId,
  };
});

test.after(async () => {
  await server.close();
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
      name: "products/polling",
      trigger: () =>
        triggerGet(context, "/products/polling/start", {
          workflowId: newFlowId("product-polling"),
          pollingCompletionThreshold: 3,
        }),
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
        return triggerGet(context, "/products/signup/submit", {
          username,
          email: `${username}@example.com`,
        });
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
      flags: defaultFlags(),
    },
    {
      name: "products/shortlist-candidates/employer-opt-in",
      trigger: async () => {
        const employerId = newFlowId("employer");
        await triggerPost(context, "/products/shortlist-candidates/opt_in", {
          employerId,
        });
        return { flowId: employerOptInFlowId(employerId), runId: "" };
      },
      flags: defaultFlags(),
    },
    {
      name: "products/shortlist-candidates/shortlist",
      trigger: async () => {
        const employerId = newFlowId("shortlist-employer");
        const candidateId = newFlowId("candidate");
        await triggerPost(context, "/products/shortlist-candidates/opt_in", { employerId });
        await triggerPost(context, "/products/shortlist-candidates/shortlist", {
          employerId,
          candidateId,
        });
        return { flowId: shortlistFlowId(employerId, candidateId), runId: "" };
      },
      flags: defaultFlags(),
    },
    {
      name: "patterns/polling/simple",
      trigger: () =>
        triggerGet(context, "/patterns/polling/start/simple", {
          workflowId: newFlowId("pattern-polling-simple"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/polling/backoff",
      trigger: () =>
        triggerGet(context, "/patterns/polling/start/backoff", {
          workflowId: newFlowId("pattern-polling-backoff"),
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
          userProfile: {
            displayName: "Smoke Tester",
            email: `${userId}@example.com`,
          },
        });
        return { flowId: result.flowId || userId, runId: result.runId };
      },
      flags: defaultFlags(),
    },
    {
      name: "patterns/intervention",
      trigger: () =>
        triggerGet(context, "/patterns/intervention/start", {
          workflowId: newFlowId("intervention"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/resettable-timer",
      trigger: () =>
        triggerGet(context, "/patterns/resettable-timer/start", {
          workflowId: newFlowId("resettable-timer"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/parallel/simple",
      trigger: () =>
        triggerGet(context, "/patterns/parallel/start/simple", {
          workflowId: newFlowId("parallel-simple"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/parallel/with-await",
      trigger: () =>
        triggerGet(context, "/patterns/parallel/start/withAwait", {
          workflowId: newFlowId("parallel-await"),
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
      flags: defaultFlags(),
    },
    {
      name: "patterns/scalable-parallel",
      trigger: () =>
        triggerGet(context, "/patterns/scalable-parallel/start", {
          workflowId: newFlowId("scalable-parallel"),
          numOfChildWfs: 1,
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/parent-child",
      trigger: () =>
        triggerGet(context, "/patterns/parent-child/start", {
          workflowId: newFlowId("parent-child"),
          numOfChildWfs: 1,
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/drain-channels/internal",
      trigger: () =>
        triggerGet(context, "/patterns/drain-channels/internal/start", {
          workflowId: newFlowId("drain-internal"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/drain-channels/signal",
      trigger: () =>
        triggerGet(context, "/patterns/drain-channels/signal/startorsignal", {
          workflowId: newFlowId("drain-signal"),
        }),
      flags: defaultFlags(),
    },
    {
      name: "patterns/wait-for-state-completion",
      trigger: () =>
        triggerGet(context, "/patterns/wait-for-state-completion/start", {
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
      flags: stepStartMayFailFlags(),
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

test("flow smoke catalog size", () => {
  const catalog = flowSmokeCatalog();
  assert.ok(catalog.length > 0, "flow smoke catalog is empty");
});

for (const entry of flowSmokeCatalog()) {
  test(`flow smoke ${entry.name}`, async () => {
    const result = await entry.trigger(context);
    assert.ok(result.flowId, `${entry.name}: controller response did not include flowID`);
    await assertFlowSmokeStartStep(entry, result.flowId, result.runId);
    await assertFlowSmokeNoUnexpectedFailures(entry, result.flowId, result.runId);
  });
}
