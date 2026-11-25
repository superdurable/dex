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

export interface ExpenseClaim {
  readonly amountCents: number;
  readonly bankAccount: string;
}

/** The provider confirmed that the request can never succeed. */
export class PayoutRejectedError extends Error {}

/** The provider did not answer in time; the same request may succeed later. */
export class PayoutTimeoutError extends Error {}

/**
 * Stands in for a payment provider API.
 * It rejects a bank account that is not eight digits at once.
 * It times out on the first submission of every valid claim, then pays it.
 */
export class FakePaymentProvider {
  private readonly submissions = new Map<string, number>();

  /** Pays a claim once per claim ID, so a retried Step does not pay twice. */
  public submitPayout(claimId: string, claim: ExpenseClaim): string {
    const submissions = (this.submissions.get(claimId) ?? 0) + 1;
    this.submissions.set(claimId, submissions);
    if (!/^\d{8}$/.test(claim.bankAccount)) {
      throw new PayoutRejectedError(`bank account ${claim.bankAccount} is invalid`);
    }
    if (submissions === 1) {
      throw new PayoutTimeoutError("payment provider timed out");
    }
    return `payout-${claimId}`;
  }

  public countSubmissions(claimId: string): number {
    return this.submissions.get(claimId) ?? 0;
  }
}
