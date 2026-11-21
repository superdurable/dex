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

/** Runs the customer refund example's scenarios against a real Dex server. */

import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { randomBytes, randomUUID } from "node:crypto";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { promisify } from "node:util";

import {
  AttributeMatch,
  Client,
  FlowDefinitionUploadOutcome,
  PayloadEncryptionError,
  Registry,
  Worker,
  WorkerInvocationError,
  stringCodec,
  type BlobCache,
  type PayloadCodec,
} from "@superdurable/dex";

import {
  FakeBillingService,
  type Order,
} from "../../src/products/customer-refund/billing-service.js";
import { FakeEmailService } from "../../src/products/customer-refund/email-service.js";
import { MockMessageModel } from "../../src/products/customer-refund/message-model.js";
import {
  AWAITING_MANAGER,
  CustomerRefundFlow,
  NO_OUTAGE_NOTE,
  WINDOW_EXPIRED_NOTE,
  caseStatus,
  customerMessage,
  refundId,
} from "../../src/products/customer-refund/refund-flow.js";
import {
  FLOW_DEFINITIONS_DIR,
  REFUND_SCENARIOS,
  newPayloadCodec,
  newRefundFlow,
  openRefundBlobCache,
  refundWorkerOptions,
  type BillingMode,
} from "../../src/products/customer-refund/refund-settings.js";

const WAIT_TIMEOUT_MS = 45_000;
const serverAddress = process.env.DEX_FLOW_SERVICE_ADDRESS ?? "127.0.0.1:8801";
const dexcli = process.env.DEXCLI_PATH ?? "dexcli";

/** Counts the payment timeouts, so a test can show they really happened. */
class TimeoutCountingBillingService extends FakeBillingService {
  public paymentTimeouts = 0;

  public override refund(order: Order, idempotencyKey: string, simulateTimeout: boolean): string {
    if (simulateTimeout) {
      this.paymentTimeouts += 1;
    }
    return super.refund(order, idempotencyKey, simulateTimeout);
  }
}

/** One test's ledgers, cache folder, and Client, shared by its Workers. */
class RefundTestbed {
  public readonly billing: FakeBillingService;
  public readonly email: FakeEmailService;
  public readonly flow: CustomerRefundFlow;
  public readonly client: Client;
  private readonly cache: BlobCache;

  public constructor(
    private readonly directory: string,
    private readonly payloadCodec: PayloadCodec | undefined,
  ) {
    this.billing = new FakeBillingService(join(directory, "billing.sqlite"));
    this.email = new FakeEmailService(join(directory, "emails.sqlite"));
    this.flow = this.newFlow("normal");
    this.cache = openRefundBlobCache(join(directory, "client-cache"));
    this.client = new Client(new Registry([this.flow]), this.cache, { serverAddress, payloadCodec });
  }

  public newFlow(billingMode: BillingMode): CustomerRefundFlow {
    return newRefundFlow(billingMode, this.billing.ledgerPath, this.email.ledgerPath);
  }

  /** Runs work while a Worker built from the Worker entrypoint's options serves the Flow. */
  public async withWorker(
    flow: CustomerRefundFlow,
    work: () => Promise<void>,
    flowDefinitionsDir: string = FLOW_DEFINITIONS_DIR,
  ): Promise<void> {
    const cache = openRefundBlobCache(join(this.directory, `worker-cache-${randomUUID()}`));
    const options = refundWorkerOptions(serverAddress, this.payloadCodec, flowDefinitionsDir);
    const worker = new Worker(new Registry([flow]), cache, options);
    try {
      await worker.start();
      await worker.waitPullRegistered({ timeoutMs: 30_000 });
      await work();
    } finally {
      await worker.close();
      cache.close();
    }
  }

  public async waitForManager(flowId: string): Promise<void> {
    await this.client.waitForAttributeMatch(flowId, caseStatus, AttributeMatch.equalTo(AWAITING_MANAGER), {
      requestTimeoutMs: WAIT_TIMEOUT_MS,
    });
  }

  public async close(): Promise<void> {
    await this.client.close();
    this.cache.close();
  }
}

async function withTestbed(work: (bed: RefundTestbed, directory: string) => Promise<void>): Promise<void> {
  const directory = await mkdtemp(join(tmpdir(), "dex-customer-refund-"));
  const bed = new RefundTestbed(directory, undefined);
  try {
    await work(bed, directory);
  } finally {
    await bed.close();
    await rm(directory, { recursive: true, force: true });
  }
}

function newFlowId(): string {
  return `refund-${randomUUID()}`;
}

test("a manager approves the Northstar refund", async () => {
  await withTestbed(async (bed) => {
    const flowId = newFlowId();
    let output = "";
    await bed.withWorker(bed.newFlow("normal"), async () => {
      await bed.client.startFlow(bed.flow, flowId, REFUND_SCENARIOS.normal);
      await bed.waitForManager(flowId);
      await bed.client.invokeRPC(bed.flow.approveRefund, flowId, { reason: "Outage confirmed" });
      const result = await bed.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
      assert.equal(result.status, "completed", result.errorMessage);
      output = result.singleOutput(stringCodec);
    });

    assert.equal(output, "refunded");
    const refunds = bed.billing.listRefunds();
    assert.deepEqual(refunds.map((refund) => [refund.orderId, refund.amountUsd]), [["INV-1249", 1249]]);
    const [storedRefundId] = await bed.client.getAttributes(flowId, refundId);
    assert.equal(storedRefundId, refunds[0]?.refundId);
    assert.deepEqual(bed.email.listSentKeys(), [`email-${flowId}`]);
  });
});

test("a manager rejects the refund", async () => {
  await withTestbed(async (bed) => {
    const flowId = newFlowId();
    let output = "";
    await bed.withWorker(bed.newFlow("normal"), async () => {
      await bed.client.startFlow(bed.flow, flowId, REFUND_SCENARIOS.normal);
      await bed.waitForManager(flowId);
      await bed.client.invokeRPC(bed.flow.rejectRefund, flowId, { reason: "Credit offered instead" });
      output = (await bed.client.waitForFlow(flowId, WAIT_TIMEOUT_MS)).singleOutput(stringCodec);
    });

    assert.equal(output, "denied");
    assert.deepEqual(bed.billing.listRefunds(), []);
    const [message] = await bed.client.getAttributes(flowId, customerMessage);
    assert.match(message ?? "", /Credit offered instead/);
  });
});

test("a blank reason is refused and a later valid decision is accepted", async () => {
  await withTestbed(async (bed) => {
    const flowId = newFlowId();
    let output = "";
    await bed.withWorker(bed.newFlow("normal"), async () => {
      await bed.client.startFlow(bed.flow, flowId, REFUND_SCENARIOS.normal);
      await bed.waitForManager(flowId);
      for (const rpc of [bed.flow.approveRefund, bed.flow.rejectRefund]) {
        await assert.rejects(
          bed.client.invokeRPC(rpc, flowId, { reason: "   " }),
          (failure: unknown) => failure instanceof WorkerInvocationError && /a reason is required/.test(failure.message),
        );
      }
      const [status] = await bed.client.getAttributes(flowId, caseStatus);
      assert.equal(status, AWAITING_MANAGER);

      await bed.client.invokeRPC(bed.flow.approveRefund, flowId, { reason: "Outage confirmed" });
      output = (await bed.client.waitForFlow(flowId, WAIT_TIMEOUT_MS)).singleOutput(stringCodec);
    });

    assert.equal(output, "refunded");
    assert.equal(bed.billing.listRefunds().length, 1);
  });
});

test("the policy denies an old charge", async () => {
  await withTestbed(async (bed) => {
    const flowId = newFlowId();
    let output = "";
    await bed.withWorker(bed.newFlow("normal"), async () => {
      await bed.client.startFlow(bed.flow, flowId, REFUND_SCENARIOS.deny);
      output = (await bed.client.waitForFlow(flowId, WAIT_TIMEOUT_MS)).singleOutput(stringCodec);
    });

    assert.equal(output, "denied");
    const [status, message] = await bed.client.getAttributes(flowId, caseStatus, customerMessage);
    assert.equal(status, "closed-denied");
    assert.ok(message?.includes(WINDOW_EXPIRED_NOTE) && message.includes(NO_OUTAGE_NOTE), message);
    assert.deepEqual(bed.billing.listRefunds(), []);
    assert.deepEqual(bed.email.listSentKeys(), [`email-${flowId}`]);
  });
});

test("the denial note names only the policy condition that applies", async () => {
  await withTestbed(async (bed) => {
    const cases = [
      { orderId: "INV-0412", note: WINDOW_EXPIRED_NOTE, absent: NO_OUTAGE_NOTE },
      { orderId: "INV-0518", note: NO_OUTAGE_NOTE, absent: WINDOW_EXPIRED_NOTE },
    ];
    await bed.withWorker(bed.newFlow("normal"), async () => {
      for (const { orderId, note, absent } of cases) {
        const flowId = newFlowId();
        await bed.client.startFlow(bed.flow, flowId, { ...REFUND_SCENARIOS.normal, orderId });
        const output = (await bed.client.waitForFlow(flowId, WAIT_TIMEOUT_MS)).singleOutput(stringCodec);
        assert.equal(output, "denied");
        const [message] = await bed.client.getAttributes(flowId, customerMessage);
        assert.ok(message?.includes(note), `${orderId}: ${message}`);
        assert.ok(!message?.includes(absent), `${orderId}: ${message}`);
      }
    });
    assert.deepEqual(bed.billing.listRefunds(), []);
  });
});

test("the refund succeeds after two payment timeouts", async () => {
  await withTestbed(async (bed) => {
    const flowId = newFlowId();
    const billing = new TimeoutCountingBillingService(bed.billing.ledgerPath);
    const flow = new CustomerRefundFlow(billing, bed.email, new MockMessageModel());
    await bed.withWorker(flow, async () => {
      await bed.client.startFlow(bed.flow, flowId, REFUND_SCENARIOS["fail-then-succeed"]);
      await bed.waitForManager(flowId);
      await bed.client.invokeRPC(bed.flow.approveRefund, flowId, { reason: "Outage confirmed" });
      const result = await bed.client.waitForFlow(flowId, 90_000);
      assert.equal(result.status, "completed", result.errorMessage);
    });

    assert.equal(billing.paymentTimeouts, 2);
    assert.equal(bed.billing.listRefunds().length, 1);
    const completions = (await readHistoryWithDexcli(flowId)).filter(
      (event) => event.type === "StepExecuteCompleted" && event.payload?.context?.stepType === "IssueRefund",
    );
    assert.equal(completions.length, 1);
    assert.equal(completions[0]?.payload?.context?.finalAttempt, 3);
    assert.equal(completions[0]?.payload?.context?.lastFailureInfo?.attempt, 2);
  });
});

test("time travel issues the refund that a missing billing call skipped", async () => {
  await withTestbed(async (bed) => {
    const flowId = newFlowId();
    await bed.withWorker(bed.newFlow("call-missing"), async () => {
      await bed.client.startFlow(bed.flow, flowId, REFUND_SCENARIOS.normal);
      await bed.waitForManager(flowId);
      await bed.client.invokeRPC(bed.flow.approveRefund, flowId, { reason: "Outage confirmed" });
      const broken = await bed.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
      assert.equal(broken.singleOutput(stringCodec), "refunded");
    });
    const [missingRefundId] = await bed.client.getAttributes(flowId, refundId);
    assert.equal(missingRefundId, "");
    assert.deepEqual(bed.billing.listRefunds(), []);

    await bed.withWorker(bed.newFlow("normal"), async () => {
      await timeTravelWithDexcli(flowId, "IssueRefund");
      const fixed = await bed.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
      assert.equal(fixed.status, "completed", fixed.errorMessage);
    });

    const refunds = bed.billing.listRefunds();
    assert.deepEqual(refunds.map((refund) => refund.idempotencyKey), [`refund-${flowId}`]);
    const [storedRefundId] = await bed.client.getAttributes(flowId, refundId);
    assert.equal(storedRefundId, refunds[0]?.refundId);
    assert.deepEqual(bed.email.listSentKeys(), [`email-${flowId}`]);
  });
});

test("time travel after a successful refund reuses its refund ID", async () => {
  await withTestbed(async (bed) => {
    const flowId = newFlowId();
    await bed.withWorker(bed.newFlow("normal"), async () => {
      await bed.client.startFlow(bed.flow, flowId, REFUND_SCENARIOS.small);
      await bed.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
      const [firstRefundId] = await bed.client.getAttributes(flowId, refundId);

      await timeTravelWithDexcli(flowId, "IssueRefund");
      const again = await bed.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
      assert.equal(again.status, "completed", again.errorMessage);
      const [secondRefundId] = await bed.client.getAttributes(flowId, refundId);
      assert.equal(secondRefundId, firstRefundId);
    });

    assert.deepEqual(bed.billing.listRefunds().map((refund) => refund.idempotencyKey), [`refund-${flowId}`]);
    assert.deepEqual(bed.email.listSentKeys(), [`email-${flowId}`]);
  });
});

test("the Worker uploads the generated design", async () => {
  await withTestbed(async (bed, directory) => {
    const designDir = join(directory, "design");
    await promisify(execFile)(dexcli, [
      "visualize",
      "src/products/customer-refund/refund-flow.ts",
      "--schema-version",
      "2.0",
      "--json",
      "--out",
      join(designDir, "customer-refund"),
    ]);
    const content = await readFile(join(designDir, "customer-refund.json"));
    const generatedAt = new Date((JSON.parse(content.toString("utf8")) as { generatedAt: string }).generatedAt);

    await bed.withWorker(bed.newFlow("normal"), async () => {
      // Uploading the same document again is UNCHANGED only when the Worker already stored it.
      const [result] = await bed.client.uploadFlowDefinitions([
        { flowType: "CustomerRefundFlow", content, generatedAt },
      ]);
      assert.equal(result?.outcome, FlowDefinitionUploadOutcome.UNCHANGED);
    }, designDir);
  });
});

test("encryption keeps case-status readable without the key", async () => {
  await withTestbed(async (plain, directory) => {
    const encrypted = new RefundTestbed(join(directory, "with-key"), await newPayloadCodec(await writePayloadKeyFile(directory)));
    try {
      const flowId = newFlowId();
      await encrypted.withWorker(encrypted.newFlow("normal"), async () => {
        await encrypted.client.startFlow(encrypted.flow, flowId, REFUND_SCENARIOS.small);
        await encrypted.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
      });
      const [message] = await encrypted.client.getAttributes(flowId, customerMessage);
      assert.match(message ?? "", /refunded 49\.00 USD/);

      const [status] = await plain.client.getAttributes(flowId, caseStatus);
      assert.equal(status, "closed-refunded");
      await assert.rejects(
        plain.client.getAttributes(flowId, customerMessage),
        (failure: unknown) => failure instanceof PayloadEncryptionError && /configure a PayloadCodec/.test(failure.message),
      );
    } finally {
      await encrypted.close();
    }
  });
});

/** Writes a key file in the dexcli codec-server keygen format. */
async function writePayloadKeyFile(directory: string): Promise<string> {
  const path = join(directory, "payload-keys.json");
  const keys = { k1: randomBytes(32).toString("base64") };
  await writeFile(path, JSON.stringify({ current_key_id: "k1", keys }), { mode: 0o600 });
  return path;
}

/** Runs the README's time travel command. */
async function timeTravelWithDexcli(flowId: string, stepType: string): Promise<void> {
  await promisify(execFile)(dexcli, [
    "flow",
    "time-travel",
    flowId,
    "--server",
    serverAddress,
    "--type",
    "step-type",
    "--target",
    stepType,
    "--reason",
    "billing call restored",
    "--yes",
  ]);
}

interface HistoryEvent {
  readonly type: string;
  readonly payload?: {
    readonly context?: {
      readonly stepType?: string;
      readonly finalAttempt?: number;
      readonly lastFailureInfo?: { readonly attempt?: number };
    };
  };
}

/** Reads the run's history as the History view shows it. */
async function readHistoryWithDexcli(flowId: string): Promise<HistoryEvent[]> {
  const { stdout } = await promisify(execFile)(
    dexcli,
    ["flow", "history", flowId, "--all", "--server", serverAddress],
    { maxBuffer: 16 * 1024 * 1024 },
  );
  return (JSON.parse(stdout) as { events: HistoryEvent[] }).events;
}
