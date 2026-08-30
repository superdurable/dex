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
import { randomUUID } from "node:crypto";
import test from "node:test";

import { loadEnv } from "../../src/config/env.js";
import { startSampleServer, type SampleServer } from "../../src/main.js";
import { userOnboardingFlow } from "../../src/products/signup/user-signup-flow.js";

let server: SampleServer;
let baseUrl: string;

test.before(async () => {
  process.env.DEX_EXAMPLES_HTTP_ADDRESS =
    process.env.DEX_SMOKE_HTTP_ADDRESS ?? "127.0.0.1:18080";
  process.env.DEX_WORKER_BIND_ADDRESS =
    process.env.DEX_SMOKE_WORKER_BIND_ADDRESS ?? "127.0.0.1:18803";
  server = await startSampleServer();
  baseUrl = `http://${loadEnv().httpAddress}`;
});

test.after(async () => {
  await server.close();
});

async function get(
  path: string,
  query: Record<string, string | number | boolean> = {},
): Promise<{ status: number; text: string; json?: unknown }> {
  const url = new URL(path, baseUrl);
  for (const [key, value] of Object.entries(query)) {
    url.searchParams.set(key, String(value));
  }
  const response = await fetch(url);
  const text = await response.text();
  let json: unknown;
  try {
    json = JSON.parse(text) as unknown;
  } catch {
    json = undefined;
  }
  return { status: response.status, text, json };
}

async function post(
  path: string,
  body?: unknown,
  query: Record<string, string> = {},
): Promise<{ status: number; text: string; json?: unknown }> {
  const url = new URL(path, baseUrl);
  for (const [key, value] of Object.entries(query)) {
    url.searchParams.set(key, value);
  }
  const response = await fetch(url, {
    method: "POST",
    headers: body === undefined ? undefined : { "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await response.text();
  let json: unknown;
  try {
    json = JSON.parse(text) as unknown;
  } catch {
    json = undefined;
  }
  return { status: response.status, text, json };
}

function id(prefix: string): string {
  return `${prefix}-${randomUUID()}`;
}

function requireOk(result: { status: number; text: string }, label: string): void {
  assert.ok(
    result.status >= 200 && result.status < 300,
    `${label} failed: ${result.status} ${result.text}`,
  );
}

test("cron schedule starter starts one finite flow", async () => {
  const { CRON_SCHEDULE_FLOW_ID, startCronSchedule } = await import(
    "../../src/config/cron-schedule-starter.js"
  );
  const { cronScheduleFlow } = await import(
    "../../src/patterns/cron/cron-schedule-flow.js"
  );
  await startCronSchedule(server.client);
  await server.client.publish(
    CRON_SCHEDULE_FLOW_ID,
    cronScheduleFlow.trigger,
    ...Array<void>(10).fill(undefined),
  );
  const result = await server.client.waitForFlow(CRON_SCHEDULE_FLOW_ID, 45_000);
  assert.equal(result.status, "completed");
});

test("product moneytransfer start", async () => {
  const result = await get("/products/money-transfer/start", {
    fromAccount: "a",
    toAccount: "b",
    amount: 10,
    notes: "smoke",
  });
  requireOk(result, "moneytransfer/start");
  assert.ok((result.json as { flowID: string }).flowID);
});

test("product order-processing start approve", async () => {
  const start = await get("/products/order-processing/start");
  requireOk(start, "order-processing/start");
  const workflowId = (start.json as { flowID: string }).flowID;
  requireOk(
    await get("/products/order-processing/approve", { workflowId }),
    "order-processing/approve",
  );
  requireOk(
    await get("/products/order-processing/describe", { workflowId }),
    "order-processing/describe",
  );
});

test("product microservice start swap signal", async () => {
  const workflowId = id("ms");
  requireOk(await get("/products/microservices/start", { workflowId }), "microservice/start");
  let swap: { status: number; text: string } | undefined;
  for (let attempt = 0; attempt < 20; attempt += 1) {
    swap = await get("/products/microservices/swap", { workflowId, data: "swapped" });
    if (swap.status >= 200 && swap.status < 300) {
      break;
    }
    await new Promise((resolve) => {
      setTimeout(resolve, 100);
    });
  }
  requireOk(swap!, "microservice/swap");
  requireOk(await get("/products/microservices/signal", { workflowId }), "microservice/signal");
});

test("product engagement start describe optout decline accept", async () => {
  const start = await get("/products/engagement/start");
  requireOk(start, "engagement/start");
  const workflowId = (start.json as { flowID: string }).flowID;
  requireOk(await get("/products/engagement/describe", { workflowId }), "engagement/describe");
  requireOk(await get("/products/engagement/optout", { workflowId }), "engagement/optout");
  requireOk(
    await get("/products/engagement/decline", { workflowId, notes: "no" }),
    "engagement/decline",
  );
  requireOk(
    await get("/products/engagement/accept", { workflowId, notes: "yes" }),
    "engagement/accept",
  );
  requireOk(
    await get("/products/engagement/list", { query: `WorkflowId="${workflowId}"` }),
    "engagement/list",
  );
});

test("product subscription start describe update cancel", async () => {
  const start = await get("/products/subscription/start");
  requireOk(start, "subscription/start");
  const workflowId = (start.json as { flowID: string }).flowID;
  requireOk(await get("/products/subscription/describe", { workflowId }), "subscription/describe");
  requireOk(
    await get("/products/subscription/updateChargeAmount", { workflowId, newChargeAmount: 250 }),
    "subscription/updateChargeAmount",
  );
  requireOk(await get("/products/subscription/cancel", { workflowId }), "subscription/cancel");
});

test("product user onboarding completes every task", async () => {
  const username = id("user").replace(/-/g, "");
  requireOk(
    await get("/products/signup/submit", { username, email: `${username}@example.com` }),
    "signup/submit",
  );
  await server.client.waitForAttributeEqual(
    username,
    userOnboardingFlow.status,
    "waiting_for_verification",
    20_000,
  );
  requireOk(await get("/products/signup/verify", { username }), "signup/verify");
  await server.client.waitForAttributeEqual(
    username,
    userOnboardingFlow.status,
    "waiting_for_task_1",
    20_000,
  );
  requireOk(
    await get("/products/signup/accomplish-task-1", { username }),
    "signup/accomplish-task-1",
  );
  await server.client.waitForAttributeEqual(
    username,
    userOnboardingFlow.status,
    "waiting_for_task_2",
    20_000,
  );
  requireOk(
    await get("/products/signup/accomplish-task-2", { username }),
    "signup/accomplish-task-2",
  );
});

test("product jobpost create read update delete search", async () => {
  const created = await get("/products/job-post/create", {
    title: "Engineer",
    description: "Build flows",
  });
  requireOk(created, "jobpost/create");
  const match = /started workflowId:\s*(.+)$/i.exec(created.text.trim());
  assert.ok(match, `unexpected create response: ${created.text}`);
  const workflowId = match[1]!.trim();
  requireOk(await get("/products/job-post/read", { workflowId }), "jobpost/read");
  requireOk(
    await get("/products/job-post/update", {
      workflowId,
      title: "Senior Engineer",
      description: "Build more flows",
      notes: "updated",
    }),
    "jobpost/update",
  );
  requireOk(await get("/products/job-post/search", { query: "Engineer" }), "jobpost/search");
  requireOk(await get("/products/job-post/delete", { workflowId }), "jobpost/delete");
});

test("product shortlist opt_in is_opted_in shortlist revoke opt_out email", async () => {
  const employerId = id("employer");
  const candidateId = id("candidate");
  requireOk(
    await post("/products/shortlist-candidates/opt_in", { employerId }),
    "shortlist opt_in",
  );
  requireOk(
    await get("/products/shortlist-candidates/is_opted_in", { employerId }),
    "shortlist is_opted_in",
  );
  requireOk(
    await post("/products/shortlist-candidates/shortlist", { employerId, candidateId }),
    "shortlist shortlist",
  );
  requireOk(
    await get("/products/shortlist-candidates/email_sent_timestamp", {
      employerId,
      candidateId,
    }),
    "shortlist email_sent_timestamp",
  );
  requireOk(
    await post("/products/shortlist-candidates/revoke_shortlist", { employerId, candidateId }),
    "shortlist revoke",
  );
  requireOk(
    await post("/products/shortlist-candidates/opt_out", { employerId }),
    "shortlist opt_out",
  );
});

test("design-pattern waitforstepcompletion start", async () => {
  requireOk(
    await get("/patterns/wait-for-step-completion/start", {
      workflowId: id("wait-state"),
    }),
    "waitforstepcompletion",
  );
});

test("design-pattern timeout start", async () => {
  requireOk(
    await get("/patterns/timeout/start", {
      workflowId: id("timeout"),
      successfulWorkflow: true,
    }),
    "timeout",
  );
});

test("design-pattern polling simple and backoff", async () => {
  requireOk(
    await get("/patterns/polling/start/timer", { workflowId: id("dp-timer") }),
    "dp simple polling",
  );
  requireOk(
    await get("/patterns/polling/start/backoff", { workflowId: id("dp-backoff") }),
    "dp backoff polling",
  );
});

test("design-pattern interruptible start cancel", async () => {
  const workflowId = id("interrupt");
  requireOk(
    await get("/patterns/interruptible/start", { workflowId }),
    "interrupt start",
  );
  requireOk(
    await get("/patterns/interruptible/cancel", { workflowId }),
    "interrupt cancel",
  );
});

test("design-pattern reminder start and optout", async () => {
  const started = await get("/patterns/reminders/start");
  requireOk(started, "reminder start");
  const match = /started workflowId:\s*(.+)$/i.exec(started.text.trim());
  assert.ok(match, started.text);
  const workflowId = match[1]!.trim();
  // Start a second reminder to exercise optout without racing accept.
  const opted = await get("/patterns/reminders/start");
  requireOk(opted, "reminder start 2");
  const optMatch = /started workflowId:\s*(.+)$/i.exec(opted.text.trim());
  assert.ok(optMatch, opted.text);
  requireOk(
    await get("/patterns/reminders/optout", {
      workflowId: optMatch[1]!.trim(),
    }),
    "reminder optout",
  );
});

test("design-pattern entity store profile lifecycle", async () => {
  const userId = id("user");
  requireOk(
    await post("/patterns/entity-store/profile", {
      userId,
      displayName: "Ada Lovelace",
      email: "ada@example.com",
      marketingOptIn: true,
      credits: 120,
      weight: 59.5,
      lastLoggedInTime: "2026-08-11T15:30:00Z",
      metadata: { source: "e2e", tags: ["example", "pro"] },
    }),
    "entity store create",
  );
  requireOk(
    await post("/patterns/entity-store/profile/update", {
      userId,
      displayName: "Ada Byron",
      email: "ada.byron@example.com",
      marketingOptIn: false,
      credits: 180,
      weight: 60.25,
      lastLoggedInTime: "2026-08-12T09:45:00Z",
      metadata: { source: "e2e", tags: ["example", "enterprise"] },
    }),
    "entity store update",
  );
  requireOk(
    await get("/patterns/entity-store/profile", { userId }),
    "entity store get",
  );
  requireOk(
    await post("/patterns/entity-store/profile/clear", undefined, { userId }),
    "entity store clear",
  );
});

test("design-pattern manual recovery start", async () => {
  requireOk(
    await get("/patterns/manual-recovery/start", { workflowId: id("manual-recovery") }),
    "manual recovery",
  );
});

test("design-pattern inactiveness tracker timer start activity", async () => {
  const workflowId = id("timer");
  requireOk(
    await get("/patterns/inactiveness-tracker-timer/start", { workflowId }),
    "timer start",
  );
  requireOk(
    await get("/patterns/inactiveness-tracker-timer/activity", { workflowId }),
    "record activity",
  );
});

test("design-pattern parallel step variants", async () => {
  requireOk(
    await get("/patterns/parallel/start/static", { workflowId: id("par-static") }),
    "parallel static",
  );
  requireOk(
    await get("/patterns/parallel/start/dynamic", {
      workflowId: id("par-dynamic"),
    }),
    "parallel dynamic",
  );
  requireOk(
    await get("/patterns/parallel/start/await", {
      workflowId: id("par-await"),
    }),
    "parallel await",
  );
  requireOk(
    await get("/patterns/parallel/start/first-win", {
      workflowId: id("par-first-win"),
    }),
    "parallel first win",
  );
});

test("design-pattern recovery start", async () => {
  requireOk(
    await get("/patterns/recovery/start", {
      workflowId: id("recovery"),
      itemName: "widget",
      quantity: 1,
    }),
    "recovery",
  );
});

test("design-pattern parallel SubFlows start", async () => {
  for (const kind of ["basic", "wait-for-half", "long-lived-parent", "short-lived-parent"]) {
    requireOk(
      await get(`/patterns/parallel-subflows/start/${kind}`, {
        workflowId: id(`parallel-subflows-${kind}`),
      }),
      `parallel SubFlows ${kind}`,
    );
  }
});

test("design-pattern drain channels", async () => {
  requireOk(
    await get("/patterns/drain-channels/internal/start", {
      workflowId: id("drain-internal"),
    }),
    "drain internal",
  );
  requireOk(
    await get("/patterns/drain-channels/external-publishing/start-or-publish", {
      workflowId: id("drain-external"),
    }),
    "drain externally published channel",
  );
});
