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

import type { MyDependencyService } from "../my-dependency-service.js";
import type { Customer } from "./models.js";

export const SubscriptionBilling = {
  sendWelcomeEmail(customer: Customer, service: MyDependencyService): void {
    service.sendEmail(customer.email, "welcome email", "hello content");
  },
  trialPeriod(customer: Customer): number {
    return customer.subscription.trialPeriodMs;
  },
  isSubscriptionOver(customer: Customer, periodNumber: number): boolean {
    return periodNumber >= customer.subscription.maxBillingPeriods;
  },
  billingPeriod(customer: Customer): number {
    return customer.subscription.billingPeriodMs;
  },
  sendSubscriptionOverEmail(customer: Customer, service: MyDependencyService): void {
    service.sendEmail(customer.email, "subscription over", "hello content");
  },
  chargeCurrentPeriod(customer: Customer, service: MyDependencyService): void {
    service.chargeUser(customer.email, customer.id, customer.subscription.billingPeriodCharge);
  },
  sendCanceledEmail(customer: Customer, service: MyDependencyService): void {
    service.sendEmail(customer.email, "subscription canceled", "hello content");
  },
  requireSingleChargeAmount(values: readonly number[] | null | undefined): number {
    if (values === null || values === undefined || values.length !== 1) {
      const count = values?.length ?? 0;
      throw new Error(`expected one charge amount, got ${count}`);
    }
    return values[0]!;
  },
  applyChargeAmount(customer: Customer, amount: number): void {
    customer.subscription.billingPeriodCharge = amount;
  },
};
