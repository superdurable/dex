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

/** Settings shared by the refund Worker, the start and decide commands, the shared server, and the tests. */

import { randomBytes } from "node:crypto";
import { existsSync, mkdirSync, statSync } from "node:fs";
import { dirname, join } from "node:path";

import {
  AesGcmPayloadCodec,
  FilePayloadEncryptionKeyProvider,
  openBlobCache,
  type BlobCache,
  type PayloadCodec,
  type WorkerOptions,
} from "@superdurable/dex";

import {
  CallMissingBillingService,
  DEFAULT_BILLING_LEDGER,
  FakeBillingService,
} from "./billing-service.js";
import { DEFAULT_EMAIL_LEDGER, FakeEmailService } from "./email-service.js";
import { MockMessageModel } from "./message-model.js";
import { CustomerRefundFlow, type RefundRequest } from "./refund-flow.js";

export const FLOW_DEFINITIONS_DIR = "build/customer-refund";

export type BillingMode = "normal" | "call-missing";

export interface RefundEnvironment {
  readonly serverAddress: string;
  readonly payloadKeyFile: string;
}

/** Reads the settings that the Worker and the commands share; each entry script calls it once. */
export function readRefundEnvironment(): RefundEnvironment {
  return {
    serverAddress: process.env.DEX_FLOW_SERVICE_ADDRESS ?? "127.0.0.1:8801",
    payloadKeyFile: process.env.REFUND_PAYLOAD_KEY_FILE ?? "",
  };
}

/** Returns the shared example server's normal-billing Flow; REFUND_LEDGER_DIR moves its ledgers. */
export function newSharedServerRefundFlow(): CustomerRefundFlow {
  const ledgerDirectory = process.env.REFUND_LEDGER_DIR || dirname(DEFAULT_BILLING_LEDGER);
  return newRefundFlow("normal", join(ledgerDirectory, "billing.sqlite"), join(ledgerDirectory, "emails.sqlite"));
}

/** Reads REFUND_BILLING_MODE; only the Worker uses it. */
export function readBillingMode(): BillingMode {
  const billingMode = process.env.REFUND_BILLING_MODE ?? "normal";
  if (billingMode !== "normal" && billingMode !== "call-missing") {
    throw new Error(`REFUND_BILLING_MODE must be normal or call-missing, not ${JSON.stringify(billingMode)}`);
  }
  return billingMode;
}

const NORTHSTAR_RENEWAL: RefundRequest = {
  customer: "Northstar Studio",
  customerEmail: "billing@northstar.example",
  orderId: "INV-1249",
  reason: "Our workspace was down for 31 hours after the renewal.",
};

/** The run inputs that the start command offers. */
export const REFUND_SCENARIOS = {
  normal: NORTHSTAR_RENEWAL,
  "fail-then-succeed": { ...NORTHSTAR_RENEWAL, paymentTimeoutsToSimulate: 2 },
  deny: { ...NORTHSTAR_RENEWAL, orderId: "INV-0388" },
  small: { ...NORTHSTAR_RENEWAL, orderId: "INV-0049" },
} satisfies Record<string, RefundRequest>;

export type RefundScenario = keyof typeof REFUND_SCENARIOS;

/** Returns an AES-GCM payload codec for the key file, or undefined without one. */
export async function newPayloadCodec(keyFile: string): Promise<PayloadCodec | undefined> {
  if (keyFile === "") {
    return undefined;
  }
  const keyProvider = await FilePayloadEncryptionKeyProvider.fromFile(keyFile);
  return new AesGcmPayloadCodec(keyProvider);
}

/** Returns the Flow; billing mode "call-missing" leaves out the billing call. */
export function newRefundFlow(
  billingMode: BillingMode = "normal",
  billingLedger: string = DEFAULT_BILLING_LEDGER,
  emailLedger: string = DEFAULT_EMAIL_LEDGER,
): CustomerRefundFlow {
  const billing = billingMode === "call-missing"
    ? new CallMissingBillingService(billingLedger)
    : new FakeBillingService(billingLedger);
  return new CustomerRefundFlow(billing, new FakeEmailService(emailLedger), new MockMessageModel());
}

/** Returns a Worker ID without the host name; it changes on every start. */
export function newWorkerId(): string {
  return `refund-worker-${randomBytes(3).toString("hex")}`;
}

/** Returns Worker options that also upload the design when it was generated. */
export function refundWorkerOptions(
  serverAddress: string,
  payloadCodec: PayloadCodec | undefined,
  flowDefinitionsDir: string = FLOW_DEFINITIONS_DIR,
): WorkerOptions {
  const designGenerated = existsSync(flowDefinitionsDir) && statSync(flowDefinitionsDir).isDirectory();
  if (!designGenerated) {
    console.log(`No design in ${flowDefinitionsDir}; see the README to generate it.`);
  }
  return {
    serverAddress,
    pull: { appWorkerID: newWorkerId() },
    flowDefinitionPaths: designGenerated ? [flowDefinitionsDir] : [],
    payloadCodec,
  };
}

/** Opens a local cache for large Flow values, up to 1 GiB. */
export function openRefundBlobCache(directory: string): BlobCache {
  mkdirSync(directory, { recursive: true });
  return openBlobCache({ directory, maxBytes: 1 << 30 });
}
