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
import { tmpdir } from "node:os";
import { join } from "node:path";

import { startSampleServer, type SampleServer } from "../../src/main.js";
import { FakeBillingService } from "../../src/products/customer-refund/billing-service.js";
import { FakeEmailService } from "../../src/products/customer-refund/email-service.js";

export interface SharedServerTestEnvironment {
  readonly server: SampleServer;
  readonly billing: FakeBillingService;
  readonly email: FakeEmailService;
  close(): Promise<void>;
}

export async function startSharedServerTestEnvironment(): Promise<SharedServerTestEnvironment> {
  const directory = await mkdtemp(join(tmpdir(), "dex-shared-refund-"));
  try {
    const server = await startSampleServer(directory);
    return {
      server,
      billing: new FakeBillingService(join(directory, "billing.sqlite")),
      email: new FakeEmailService(join(directory, "emails.sqlite")),
      async close() {
        try {
          await server.close();
        } finally {
          await rm(directory, { recursive: true, force: true });
        }
      },
    };
  } catch (error) {
    await rm(directory, { recursive: true, force: true });
    throw error;
  }
}

export function assertRecordedRefund(
  environment: SharedServerTestEnvironment,
  flowId: string,
): string {
  const refundKey = `refund-${flowId}`;
  const refunds = environment.billing.listRefunds().filter((refund) => refund.idempotencyKey === refundKey);
  assert.equal(refunds.length, 1, refundKey);
  const [refund] = refunds;
  assert.ok(refund);
  const emailKey = `email-${flowId}`;
  assert.deepEqual(environment.email.listSentKeys().filter((key) => key === emailKey), [emailKey]);
  assert.match(refund.refundId, /^rf_\d+$/);
  return refund.refundId;
}
