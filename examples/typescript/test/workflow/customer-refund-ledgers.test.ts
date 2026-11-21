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
import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { promisify } from "node:util";

import { FakeBillingService } from "../../src/products/customer-refund/billing-service.js";
import { FakeEmailService } from "../../src/products/customer-refund/email-service.js";

const PROCESSES = 8;
const CALLS_PER_PROCESS = 5;
const billingModule = new URL("../../src/products/customer-refund/billing-service.js", import.meta.url).href;
const emailModule = new URL("../../src/products/customer-refund/email-service.js", import.meta.url).href;

/** Runs a module script in a separate Node.js process and returns its standard output. */
async function runInProcess(script: string): Promise<string> {
  const { stdout } = await promisify(execFile)(process.execPath, ["--no-warnings", "--input-type=module", "-e", script]);
  return stdout;
}

function refundScript(ledgerPath: string, idempotencyKeys: readonly string[]): string {
  return `
    import { FakeBillingService } from ${JSON.stringify(billingModule)};
    const billing = new FakeBillingService(${JSON.stringify(ledgerPath)});
    const order = billing.getOrder("INV-0049");
    for (const key of ${JSON.stringify(idempotencyKeys)}) {
      billing.refund(order, key, false);
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

function countLines(outputs: readonly string[], prefix: string): number {
  return outputs.flatMap((output) => output.split("\n")).filter((line) => line.startsWith(prefix)).length;
}

test("concurrent refunds with the same key record one refund", async () => {
  await withLedgerDirectory(async (directory) => {
    const ledgerPath = join(directory, "billing.sqlite");
    const keys = Array.from({ length: CALLS_PER_PROCESS }, () => "refund-same");
    const outputs = await Promise.all(
      Array.from({ length: PROCESSES }, () => runInProcess(refundScript(ledgerPath, keys))),
    );

    const refunds = new FakeBillingService(ledgerPath).listRefunds();
    assert.deepEqual(refunds.map((refund) => [refund.refundId, refund.idempotencyKey]), [["rf_1042", "refund-same"]]);
    assert.equal(countLines(outputs, "Refunded "), 1);
    assert.equal(countLines(outputs, "Refund rf_1042 already issued"), PROCESSES * CALLS_PER_PROCESS - 1);
  });
});

test("concurrent refunds with different keys get distinct refund IDs", async () => {
  await withLedgerDirectory(async (directory) => {
    const ledgerPath = join(directory, "billing.sqlite");
    const outputs = await Promise.all(
      Array.from({ length: PROCESSES }, (_, processIndex) => {
        const keys = Array.from({ length: CALLS_PER_PROCESS }, (_, call) => `refund-${processIndex}-${call}`);
        // Each key is sent twice, so first calls and repeats interleave across processes.
        return runInProcess(refundScript(ledgerPath, [...keys, ...keys]));
      }),
    );

    const refunds = new FakeBillingService(ledgerPath).listRefunds();
    const total = PROCESSES * CALLS_PER_PROCESS;
    assert.equal(refunds.length, total);
    assert.equal(new Set(refunds.map((refund) => refund.idempotencyKey)).size, total);
    assert.deepEqual(
      refunds.map((refund) => refund.refundId).sort(),
      Array.from({ length: total }, (_, index) => `rf_${1042 + index}`).sort(),
    );
    assert.equal(countLines(outputs, "Refunded "), total);
  });
});

test("concurrent emails with the same key send one email", async () => {
  await withLedgerDirectory(async (directory) => {
    const ledgerPath = join(directory, "emails.sqlite");
    const script = `
      import { FakeEmailService } from ${JSON.stringify(emailModule)};
      const email = new FakeEmailService(${JSON.stringify(ledgerPath)});
      for (let call = 0; call < ${CALLS_PER_PROCESS}; call += 1) {
        email.send("billing@northstar.example", "Hi", "email-same");
        email.send("billing@northstar.example", "Hi", "email-" + process.pid + "-" + call);
      }
    `;
    const outputs = await Promise.all(Array.from({ length: PROCESSES }, () => runInProcess(script)));

    const sentKeys = new FakeEmailService(ledgerPath).listSentKeys();
    assert.equal(sentKeys.filter((key) => key === "email-same").length, 1);
    assert.equal(sentKeys.length, 1 + PROCESSES * CALLS_PER_PROCESS);
    assert.equal(countLines(outputs, "Emailed "), 1 + PROCESSES * CALLS_PER_PROCESS);
  });
});
