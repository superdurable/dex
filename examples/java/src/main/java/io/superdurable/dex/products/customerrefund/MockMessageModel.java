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

package io.superdurable.dex.products.customerrefund;

/** A deterministic stand-in for a language model; it needs no API key or network call. */
public class MockMessageModel {
    public String writeMessage(
            final String customer, final String outcome, final double amountUsd, final String note) {
        if ("refunded".equals(outcome)) {
            return ("Hi " + customer + ", we are sorry for the service interruption. "
                    + "We refunded " + FakeBillingService.formatUsd(amountUsd) + " USD to your card. "
                    + note).strip();
        }
        return ("Hi " + customer + ", thank you for reaching out. We cannot refund this charge. "
                + note).strip();
    }
}
