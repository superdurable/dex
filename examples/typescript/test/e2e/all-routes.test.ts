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

test("cron schedule starter registered sample flow", async () => {
  // Cron schedules are Temporal schedules; describeFlow may not see the ID.
  // Assert the starter path is idempotent (ignore already-registered).
  const { startCronSchedule } = await import(
    "../../src/config/cron-schedule-starter.js"
  );
  await startCronSchedule(server.client);
});

test("product moneytransfer start", async () => {
  const result = await get("/moneytransfer/start", {
    fromAccount: "a",
    toAccount: "b",
    amount: 10,
    notes: "smoke",
  });
  requireOk(result, "moneytransfer/start");
  assert.ok((result.json as { flowID: string }).flowID);
});

test("product microservice start swap signal", async () => {
  const workflowId = id("ms");
  requireOk(await get("/microservice/start", { workflowId }), "microservice/start");
  let swap: { status: number; text: string } | undefined;
  for (let attempt = 0; attempt < 20; attempt += 1) {
    swap = await get("/microservice/swap", { workflowId, data: "swapped" });
    if (swap.status >= 200 && swap.status < 300) {
      break;
    }
    await new Promise((resolve) => {
      setTimeout(resolve, 100);
    });
  }
  requireOk(swap!, "microservice/swap");
  requireOk(await get("/microservice/signal", { workflowId }), "microservice/signal");
});

test("product engagement start describe optout decline accept", async () => {
  const start = await get("/engagement/start");
  requireOk(start, "engagement/start");
  const workflowId = (start.json as { flowID: string }).flowID;
  requireOk(await get("/engagement/describe", { workflowId }), "engagement/describe");
  requireOk(await get("/engagement/optout", { workflowId }), "engagement/optout");
  requireOk(
    await get("/engagement/decline", { workflowId, notes: "no" }),
    "engagement/decline",
  );
  requireOk(
    await get("/engagement/accept", { workflowId, notes: "yes" }),
    "engagement/accept",
  );
});

test("product subscription start describe update cancel", async () => {
  const start = await get("/subscription/start");
  requireOk(start, "subscription/start");
  const workflowId = (start.json as { flowID: string }).flowID;
  requireOk(await get("/subscription/describe", { workflowId }), "subscription/describe");
  requireOk(
    await get("/subscription/updateChargeAmount", { workflowId, newChargeAmount: 250 }),
    "subscription/updateChargeAmount",
  );
  requireOk(await get("/subscription/cancel", { workflowId }), "subscription/cancel");
});

test("product polling start complete", async () => {
  const workflowId = id("poll");
  requireOk(
    await get("/polling/start", { workflowId, pollingCompletionThreshold: 3 }),
    "polling/start",
  );
  requireOk(
    await get("/polling/complete", { workflowId, channel: "task-a-completed" }),
    "polling/complete-a",
  );
  requireOk(
    await get("/polling/complete", { workflowId, channel: "task-b-completed" }),
    "polling/complete-b",
  );
});

test("product signup submit verify", async () => {
  const username = id("user").replace(/-/g, "");
  requireOk(
    await get("/signup/submit", { username, email: `${username}@example.com` }),
    "signup/submit",
  );
  requireOk(await get("/signup/verify", { username }), "signup/verify");
});

test("product jobpost create read update delete search", async () => {
  const created = await get("/jobpost/create", {
    title: "Engineer",
    description: "Build flows",
  });
  requireOk(created, "jobpost/create");
  const match = /started workflowId:\s*(.+)$/i.exec(created.text.trim());
  assert.ok(match, `unexpected create response: ${created.text}`);
  const workflowId = match[1]!.trim();
  requireOk(await get("/jobpost/read", { workflowId }), "jobpost/read");
  requireOk(
    await get("/jobpost/update", {
      workflowId,
      title: "Senior Engineer",
      description: "Build more flows",
      notes: "updated",
    }),
    "jobpost/update",
  );
  requireOk(await get("/jobpost/search", { query: "Engineer" }), "jobpost/search");
  requireOk(await get("/jobpost/delete", { workflowId }), "jobpost/delete");
});

test("product shortlist opt_in is_opted_in shortlist revoke opt_out email", async () => {
  const employerId = id("employer");
  const candidateId = id("candidate");
  requireOk(
    await post("/shortlist_candidates/opt_in", { employerId }),
    "shortlist opt_in",
  );
  requireOk(
    await get("/shortlist_candidates/is_opted_in", { employerId }),
    "shortlist is_opted_in",
  );
  requireOk(
    await post("/shortlist_candidates/shortlist", { employerId, candidateId }),
    "shortlist shortlist",
  );
  requireOk(
    await get("/shortlist_candidates/email_sent_timestamp", {
      employerId,
      candidateId,
    }),
    "shortlist email_sent_timestamp",
  );
  requireOk(
    await post("/shortlist_candidates/revoke_shortlist", { employerId, candidateId }),
    "shortlist revoke",
  );
  requireOk(
    await post("/shortlist_candidates/opt_out", { employerId }),
    "shortlist opt_out",
  );
});

test("design-pattern waitforstatecompletion start", async () => {
  requireOk(
    await get("/design-pattern/waitforstatecompletion/start", {
      workflowId: id("wait-state"),
    }),
    "waitforstatecompletion",
  );
});

test("design-pattern timeout start", async () => {
  requireOk(
    await get("/design-pattern/timeout/start", {
      workflowId: id("timeout"),
      successfulWorkflow: true,
    }),
    "timeout",
  );
});

test("design-pattern polling simple and backoff", async () => {
  requireOk(
    await get("/design-pattern/polling/start/simple", { workflowId: id("dp-simple") }),
    "dp simple polling",
  );
  requireOk(
    await get("/design-pattern/polling/start/backoff", { workflowId: id("dp-backoff") }),
    "dp backoff polling",
  );
});

test("design-pattern interruptible start cancel", async () => {
  const workflowId = id("interrupt");
  requireOk(
    await get("/design-pattern/interruptible/start", { workflowId }),
    "interrupt start",
  );
  requireOk(
    await get("/design-pattern/interruptible/cancel", { workflowId }),
    "interrupt cancel",
  );
});

test("design-pattern reminder start accept optout", async () => {
  const started = await get("/design-pattern/workflow-with-reminder/start");
  requireOk(started, "reminder start");
  const match = /started workflowId:\s*(.+)$/i.exec(started.text.trim());
  assert.ok(match, started.text);
  const workflowId = match[1]!.trim();
  // Start a second reminder to exercise optout without racing accept.
  const opted = await get("/design-pattern/workflow-with-reminder/start");
  requireOk(opted, "reminder start 2");
  const optMatch = /started workflowId:\s*(.+)$/i.exec(opted.text.trim());
  assert.ok(optMatch, opted.text);
  requireOk(
    await get("/design-pattern/workflow-with-reminder/accept", { workflowId }),
    "reminder accept",
  );
  requireOk(
    await get("/design-pattern/workflow-with-reminder/optout", {
      workflowId: optMatch[1]!.trim(),
    }),
    "reminder optout",
  );
});

test("design-pattern entity store profile lifecycle", async () => {
  const userId = id("user");
  requireOk(
    await post("/design-pattern/entity-store/profile", {
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
    await post("/design-pattern/entity-store/profile/update", {
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
    await get("/design-pattern/entity-store/profile", { userId }),
    "entity store get",
  );
  requireOk(
    await post("/design-pattern/entity-store/profile/clear", undefined, { userId }),
    "entity store clear",
  );
});

test("design-pattern intervention start", async () => {
  requireOk(
    await get("/design-pattern/intervention/start", { workflowId: id("intervention") }),
    "intervention",
  );
});

test("design-pattern resettable timer start reset", async () => {
  const workflowId = id("timer");
  requireOk(
    await get("/design-pattern/resettabletimer/start", { workflowId }),
    "timer start",
  );
  requireOk(
    await get("/design-pattern/resettabletimer/reset", { workflowId }),
    "timer reset",
  );
});

test("design-pattern parallel simple and withAwait", async () => {
  requireOk(
    await get("/design-pattern/parallel/start/simple", { workflowId: id("par-simple") }),
    "parallel simple",
  );
  requireOk(
    await get("/design-pattern/parallel/start/withAwait", {
      workflowId: id("par-await"),
      countOfJobSeekers: 2,
    }),
    "parallel await",
  );
});

test("design-pattern recovery start", async () => {
  requireOk(
    await get("/design-pattern/recovery/start", {
      workflowId: id("recovery"),
      itemName: "widget",
      quantity: 1,
    }),
    "recovery",
  );
});

test("design-pattern scalableparallel start", async () => {
  requireOk(
    await get("/design-pattern/scalableparallel/start", {
      workflowId: id("scalable"),
      numOfChildWfs: 2,
    }),
    "scalableparallel",
  );
});

test("design-pattern parentchild start", async () => {
  requireOk(
    await get("/design-pattern/parentchild/start", {
      workflowId: id("parent"),
      numOfChildWfs: 2,
    }),
    "parentchild",
  );
});

test("design-pattern drain channels", async () => {
  requireOk(
    await get("/design-pattern/drainchannels/internal/start", {
      workflowId: id("drain-internal"),
    }),
    "drain internal",
  );
  requireOk(
    await get("/design-pattern/drainchannels/signal/startorsignal", {
      workflowId: id("drain-signal"),
    }),
    "drain signal",
  );
});
