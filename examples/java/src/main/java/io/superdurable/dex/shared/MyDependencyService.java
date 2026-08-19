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

package io.superdurable.dex.shared;

import org.springframework.stereotype.Component;

@Component
public class MyDependencyService {

    public void updateExternalSystem(final String message) {
        System.out.println(
                "Update external system(like via RPC, or sending Kafka message or database): "
                        + message);
    }

    public void sendEmail(final String recipient, final String subject, final String content) {
        System.out.printf(
                "sending an email to %s, title: %s, content: %s %n",
                recipient,
                subject,
                content);
    }

    public void chargeUser(final String email, final String customerId, final int amount) {
        System.out.printf(
                "charge user customerID[%s] email[%s] for $%d %n",
                customerId,
                email,
                amount);
    }

    public void shipItem(final String orderId, final boolean testFailAtShipping) {
        if (testFailAtShipping) {
            throw new RuntimeException("ship failed for order " + orderId);
        }
        System.out.println("ship item " + orderId);
    }

    public void callAPI1(final String data) {
        System.out.println("call API1");
    }

    public void callAPI2(final String data) {
        System.out.println("call API2");
    }

    public void callAPI3(final String data) {
        System.out.println("call API3");
    }

    public void callAPI4(final String data) {
        System.out.println("call API4");
    }

    public boolean checkBalance(final String account, final int amount) {
        return true;
    }

    public void debit(final String account, final int amount) {
    }

    public void credit(final String account, final int amount) {
    }

    public void createDebitMemo(final String account, final int amount, final String notes) {
    }

    public void createCreditMemo(final String account, final int amount, final String notes) {
    }

    public void undoDebit(final String account, final int amount) {
    }

    public void undoCredit(final String account, final int amount) {
    }

    public void undoCreateDebitMemo(final String account, final int amount, final String notes) {
    }

    public void undoCreateCreditMemo(final String account, final int amount, final String notes) {
    }
}
