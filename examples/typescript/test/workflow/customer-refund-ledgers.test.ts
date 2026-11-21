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

/** Checks that the refund example's fake ledgers stay correct under concurrent processes. */

import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { DatabaseSync } from "node:sqlite";
import test from "node:test";

import {
  FakeBillingService,
  RefundKeyConflict,
} from "../../src/products/customer-refund/billing-service.js";
import { FakeEmailService } from "../../src/products/customer-refund/email-service.js";

const PROCESSES = 8;
const CALLS_PER_PROCESS = 5;
const billingModule = new URL("../../src/products/customer-refund/billing-service.js", import.meta.url).href;
const emailModule = new URL("../../src/products/customer-refund/email-service.js", import.meta.url).href;

interface ChildRun {
  readonly ready: Promise<void>;
  readonly finished: Promise<string[]>;
  readonly resultLinesBeforeRelease: () => number;
}

/** Starts a module script in a child process; it prints READY, then one RESULT line per call. */
function startChild(script: string): ChildRun {
  const child = spawn(process.execPath, ["--no-warnings", "--input-type=module", "-e", script], {
    stdio: ["ignore", "pipe", "inherit"],
  });
  let output = "";
  let signalReady: () => void = () => {};
  const ready = new Promise<void>((resolve) => {
    signalReady = resolve;
  });
  child.stdout.setEncoding("utf8");
  child.stdout.on("data", (chunk: string) => {
    output += chunk;
    if (output.includes("READY\n")) {
      signalReady();
    }
  });
  const finished = new Promise<string[]>((resolve, reject) => {
    child.on("error", reject);
    child.on("close", (code) => {
      if (code !== 0) {
        reject(new Error(`child exited with ${code}: ${output}`));
        return;
      }
      resolve(resultLines(output));
    });
  });
  return { ready, finished, resultLinesBeforeRelease: () => resultLines(output).length };
}

function resultLines(output: string): string[] {
  return output.split("\n").filter((line) => line.startsWith("RESULT ")).map((line) => line.slice("RESULT ".length));
}

/**
 * Holds the ledger's write lock while the children start, then releases it.
 * Returns each child's RESULT lines.
 */
async function runWhileLedgerLocked(ledgerPath: string, scripts: readonly string[]): Promise<string[][]> {
  const holder = new DatabaseSync(ledgerPath, { timeout: 10_000 });
  holder.exec("BEGIN IMMEDIATE");
  let children: ChildRun[] = [];
  try {
    children = scripts.map(startChild);
    await Promise.all(children.map((child) => child.ready));
    // Give each child time to reach the lock after it printed READY.
    await new Promise((resolve) => setTimeout(resolve, 300));
    assert.deepEqual(children.map((child) => child.resultLinesBeforeRelease()), scripts.map(() => 0));
  } finally {
    holder.exec("ROLLBACK");
    holder.close();
  }
  return Promise.all(children.map((child) => child.finished));
}

function refundScript(ledgerPath: string, idempotencyKeys: readonly string[]): string {
  return `
    import { FakeBillingService } from ${JSON.stringify(billingModule)};
    const billing = new FakeBillingService(${JSON.stringify(ledgerPath)});
    const order = billing.getOrder("INV-0049");
    process.stdout.write("READY\\n");
    for (const key of ${JSON.stringify(idempotencyKeys)}) {
      const refundId = billing.refund(order, key, false);
      process.stdout.write("RESULT " + key + " " + refundId + "\\n");
    }
  `;
}

async function withLedgerDirectory(work: (directory: string) => Promise<void>): Promise<void> {
  const directory = await mkdtemp(join(tmpdir(), "dex-customer-refund-ledgers-"));
  try {
    await work(directory);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
}

test("concurrent refunds with the same key record one refund", async () => {
  await withLedgerDirectory(async (directory) => {
    const ledgerPath = join(directory, "billing.sqlite");
    const billing = new FakeBillingService(ledgerPath);
    billing.listRefunds();
    const keys = Array.from({ length: CALLS_PER_PROCESS }, () => "refund-same");
    const results = await runWhileLedgerLocked(ledgerPath, Array.from({ length: PROCESSES }, () => refundScript(ledgerPath, keys)));

    assert.deepEqual(results.flat(), Array.from({ length: PROCESSES * CALLS_PER_PROCESS }, () => "refund-same rf_1042"));
    const refunds = billing.listRefunds();
    assert.deepEqual(refunds.map((refund) => [refund.refundId, refund.idempotencyKey]), [["rf_1042", "refund-same"]]);
  });
});

test("concurrent refunds with different keys get distinct refund IDs", async () => {
  await withLedgerDirectory(async (directory) => {
    const ledgerPath = join(directory, "billing.sqlite");
    const billing = new FakeBillingService(ledgerPath);
    billing.listRefunds();
    const scripts = Array.from({ length: PROCESSES }, (_, processIndex) => {
      const keys = Array.from({ length: CALLS_PER_PROCESS }, (_, call) => `refund-${processIndex}-${call}`);
      // Each key is sent twice, so the repeat must return the first call's ID.
      return refundScript(ledgerPath, [...keys, ...keys]);
    });
    const results = await runWhileLedgerLocked(ledgerPath, scripts);

    const recorded = new Map(billing.listRefunds().map((refund) => [refund.idempotencyKey, refund.refundId]));
    const total = PROCESSES * CALLS_PER_PROCESS;
    assert.equal(recorded.size, total);
    assert.deepEqual(
      [...recorded.values()].sort(),
      Array.from({ length: total }, (_, index) => `rf_${1042 + index}`).sort(),
    );
    for (const line of results.flat()) {
      const [key, refundId] = line.split(" ");
      assert.equal(refundId, recorded.get(key ?? ""), line);
    }
    assert.equal(results.flat().length, 2 * total);
  });
});

test("concurrent emails with the same key send one email", async () => {
  await withLedgerDirectory(async (directory) => {
    const ledgerPath = join(directory, "emails.sqlite");
    const email = new FakeEmailService(ledgerPath);
    email.listSentKeys();
    const script = `
      import { FakeEmailService } from ${JSON.stringify(emailModule)};
      const email = new FakeEmailService(${JSON.stringify(ledgerPath)});
      process.stdout.write("READY\\n");
      for (let call = 0; call < ${CALLS_PER_PROCESS}; call += 1) {
        process.stdout.write("RESULT same " + email.send("billing@northstar.example", "Hi", "email-same") + "\\n");
        process.stdout.write("RESULT own " + email.send("billing@northstar.example", "Hi", "email-" + process.pid + "-" + call) + "\\n");
      }
    `;
    const results = (await runWhileLedgerLocked(ledgerPath, Array.from({ length: PROCESSES }, () => script))).flat();

    assert.equal(results.filter((line) => line === "same true").length, 1);
    assert.equal(results.filter((line) => line === "own true").length, PROCESSES * CALLS_PER_PROCESS);
    const sentKeys = email.listSentKeys();
    assert.equal(sentKeys.filter((key) => key === "email-same").length, 1);
    assert.equal(sentKeys.length, 1 + PROCESSES * CALLS_PER_PROCESS);
  });
});

test("deleting refund rows never reuses an ID or allows a second refund", async () => {
  await withLedgerDirectory(async (directory) => {
    const ledgerPath = join(directory, "billing.sqlite");
    const billing = new FakeBillingService(ledgerPath);
    const order = billing.getOrder("INV-0049");
    assert.equal(billing.refund(order, "refund-a", false), "rf_1042");
    assert.equal(billing.refund(order, "refund-b", false), "rf_1043");

    const ledger = new DatabaseSync(ledgerPath);
    ledger.prepare("DELETE FROM refunds WHERE idempotency_key = ?").run("refund-a");
    assert.equal(billing.refund(order, "refund-c", false), "rf_1044");
    assert.equal(billing.refund(order, "refund-a", false), "rf_1042");
    ledger.prepare("DELETE FROM refunds WHERE idempotency_key = ?").run("refund-c");
    ledger.close();
    assert.equal(billing.refund(order, "refund-d", false), "rf_1045");

    assert.deepEqual(billing.listRefunds().map((refund) => refund.refundId), ["rf_1043", "rf_1045"]);
  });
});

test("a key reused for another order is refused", async () => {
  await withLedgerDirectory(async (directory) => {
    const billing = new FakeBillingService(join(directory, "billing.sqlite"));
    billing.refund(billing.getOrder("INV-0049"), "refund-refund1", false);
    assert.throws(
      () => billing.refund(billing.getOrder("INV-1249"), "refund-refund1", false),
      (failure: unknown) => failure instanceof RefundKeyConflict && /already refunded INV-0049/.test(failure.message),
    );
    assert.deepEqual(billing.listRefunds().map((refund) => refund.orderId), ["INV-0049"]);
  });
});
