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

export interface Subscription {
  trialPeriodMs: number;
  billingPeriodMs: number;
  maxBillingPeriods: number;
  billingPeriodCharge: number;
}

export interface Customer {
  firstName: string;
  lastName: string;
  id: string;
  email: string;
  subscription: Subscription;
}

export function decodeSubscription(value: unknown): Subscription {
  const record = value as Subscription;
  return {
    trialPeriodMs: Number(record.trialPeriodMs),
    billingPeriodMs: Number(record.billingPeriodMs),
    maxBillingPeriods: Number(record.maxBillingPeriods),
    billingPeriodCharge: Number(record.billingPeriodCharge),
  };
}

export function decodeCustomer(value: unknown): Customer {
  const record = value as Customer;
  return {
    firstName: String(record.firstName),
    lastName: String(record.lastName),
    id: String(record.id),
    email: String(record.email),
    subscription: decodeSubscription(record.subscription),
  };
}
