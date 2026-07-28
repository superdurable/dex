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

package io.iworkflow.workflow;

import io.iworkflow.workflow.money.transfer.TransferRequest;
import org.springframework.stereotype.Component;

import java.util.Optional;

/**
 * This is to demonstrate how to depend on other services in the workflow code
 */
@Component
public class MyDependencyService {

    private int updateExternalCounter = 0;

    public void updateExternalSystem(final String message) {
        if (updateExternalCounter++ <= 2) {
            throw new RuntimeException("hey this is an error when calling external system, you should retry it");
        } else {
            updateExternalCounter = 0;
        }
        System.out.printf("update external system(like via RPC, or sending Kafka message or database): %s", message);
    }

    public void sendEmail(final String recipient, final String subject, final String content) {
        System.out.printf("send an email to %s, title: %s, content: %s %n", recipient, subject, content);
    }

    public void chargeUser(final String email, final String customerId, final int amount) {
        System.out.printf("charge user customerId[%s] email[%s] for $%d \n", customerId, email, amount);
    }

    public void callAPI1(String someData) {
        System.out.println("external API#1 is called");
    }

    public void callAPI2(String someData) {
        System.out.println("external API#2 is called");
    }

    public void callAPI3(String someData) {
        System.out.println("external API#3 is called");
    }

    public void callAPI4(String someData) {
        System.out.println("external API#4 is called");
    }

    public boolean checkBalance(final String fromAccount, int amount) {
        return true;
    }

    public void createDebitMemo(final String fromAccount, int amount, Optional<String> notes) {
        // throw some errors here to test the retry and recovery mechanism
        return;
    }

    public void debit(final String fromAccount, int amount) {
        // throw some errors here to test the retry and recovery mechanism
        return;
    }

    public void createCreditMemo(final String toAccount, int amount, Optional<String> notes) {
        // throw some errors here to test the retry and recovery mechanism
        return;
    }

    public void credit(final String toAccount, int amount) {
        // throw some errors here to test the retry and recovery mechanism
        return;
    }

    public void undoCreateDebitMemo(final String fromAccount, int amount, Optional<String> notes) {
        // throw some errors here to test the retry and recovery mechanism
        return;
    }

    public void undoDebit(final String fromAccount, int amount) {
        // throw some errors here to test the retry and recovery mechanism
        return;
    }

    public void undoCreateCreditMemo(final String toAccount, int amount, Optional<String> notes) {
        // throw some errors here to test the retry and recovery mechanism
        return;
    }

    public void undoCredit(final String toAccount, int amount) {
        // throw some errors here to test the retry and recovery mechanism
        return;
    }
}
