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

/** A deterministic stand-in for a language model that drafts customer messages. */

import { formatUsd } from "./billing-service.js";

/** Answers like a model would, without an API key or a network call. */
export class MockMessageModel {
  public writeMessage(customer: string, outcome: string, amountUsd: number, note: string): string {
    if (outcome === "refunded") {
      return (
        `Hi ${customer}, we are sorry for the service interruption. ` +
        `We refunded ${formatUsd(amountUsd)} USD to your card. ${note}`
      ).trim();
    }
    return `Hi ${customer}, thank you for reaching out. We cannot refund this charge. ${note}`.trim();
  }
}
