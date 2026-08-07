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

import { MyDependencyService } from "../../../src/workflow/my-dependency-service.js";
import type { Customer } from "../../../src/workflow/subscription/models.js";
import { SubscriptionBilling } from "../../../src/workflow/subscription/subscription-billing.js";

const TEST_CUSTOMER: Customer = {
  firstName: "Quanzheng",
  lastName: "Long",
  id: "123",
  email: "qlong.seattle@gmail.com",
  subscription: {
    trialPeriodMs: 2_000,
    billingPeriodMs: 1_000,
    maxBillingPeriods: 10,
    billingPeriodCharge: 100,
  },
};

class RecordingService extends MyDependencyService {
  public readonly emails: Array<{
    recipient: string;
    subject: string;
    content: string;
  }> = [];
  public readonly charges: Array<{
    email: string;
    customerId: string;
    amount: number;
  }> = [];

  public override sendEmail(recipient: string, subject: string, content: string): void {
    this.emails.push({ recipient, subject, content });
  }

  public override chargeUser(email: string, customerId: string, amount: number): void {
    this.charges.push({ email, customerId, amount });
  }
}

test("sendWelcomeEmail", () => {
  const service = new RecordingService();
  SubscriptionBilling.sendWelcomeEmail(TEST_CUSTOMER, service);
  assert.deepEqual(service.emails, [
    {
      recipient: TEST_CUSTOMER.email,
      subject: "welcome email",
      content: "hello content",
    },
  ]);
  assert.equal(SubscriptionBilling.trialPeriod(TEST_CUSTOMER), 2_000);
});

test("isSubscriptionOver", () => {
  assert.equal(SubscriptionBilling.isSubscriptionOver(TEST_CUSTOMER, 0), false);
  assert.equal(
    SubscriptionBilling.isSubscriptionOver(
      TEST_CUSTOMER,
      TEST_CUSTOMER.subscription.maxBillingPeriods,
    ),
    true,
  );
  assert.equal(SubscriptionBilling.billingPeriod(TEST_CUSTOMER), 1_000);
});

test("chargeCurrentPeriod", () => {
  const service = new RecordingService();
  SubscriptionBilling.chargeCurrentPeriod(TEST_CUSTOMER, service);
  assert.deepEqual(service.charges, [
    {
      email: TEST_CUSTOMER.email,
      customerId: TEST_CUSTOMER.id,
      amount: 100,
    },
  ]);
  assert.equal(service.emails.length, 0);
});

test("sendSubscriptionOverEmail", () => {
  const service = new RecordingService();
  SubscriptionBilling.sendSubscriptionOverEmail(TEST_CUSTOMER, service);
  assert.equal(service.charges.length, 0);
  assert.deepEqual(service.emails, [
    {
      recipient: TEST_CUSTOMER.email,
      subject: "subscription over",
      content: "hello content",
    },
  ]);
});

test("sendCanceledEmail", () => {
  const service = new RecordingService();
  SubscriptionBilling.sendCanceledEmail(TEST_CUSTOMER, service);
  assert.deepEqual(service.emails, [
    {
      recipient: TEST_CUSTOMER.email,
      subject: "subscription canceled",
      content: "hello content",
    },
  ]);
});

test("applyChargeAmount", () => {
  const customer: Customer = {
    ...TEST_CUSTOMER,
    subscription: { ...TEST_CUSTOMER.subscription },
  };
  SubscriptionBilling.applyChargeAmount(customer, 200);
  assert.equal(customer.subscription.billingPeriodCharge, 200);
  assert.equal(SubscriptionBilling.requireSingleChargeAmount([200]), 200);
});

test("requireSingleChargeAmountRejectsUnexpectedResults", () => {
  assert.throws(() => SubscriptionBilling.requireSingleChargeAmount(null));
  assert.throws(() => SubscriptionBilling.requireSingleChargeAmount([]));
  assert.throws(() => SubscriptionBilling.requireSingleChargeAmount([100, 200]));
});
