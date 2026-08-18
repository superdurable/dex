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

package io.superdurable.dex.patterns.recovery;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import org.springframework.stereotype.Component;

import java.util.Random;

@Component
public class FailureRecoveryFlow implements Flow<FailureRecoveryWorkflowInput> {
    public static final String WORKFLOW_INPUT_KEY = "workflow-input-data-attribute-key";

    public final Attribute<FailureRecoveryWorkflowInput> workflowInput =
            Attribute.define(WORKFLOW_INPUT_KEY, FailureRecoveryWorkflowInput.class);

    private final DatabaseConnection database = new DatabaseConnection();
    private final PaymentProcessor paymentProcessor = new PaymentProcessor();

    private final UpdateQuantityRecovery updateQuantityRecovery = new UpdateQuantityRecovery();
    private final VoidPaymentRecovery voidPaymentRecovery = new VoidPaymentRecovery();
    private final UpdateItemQuantity updateItemQuantity = new UpdateItemQuantity();
    private final ChargeForItems chargeForItems = new ChargeForItems();

    @Override
    public StepList<FailureRecoveryWorkflowInput> getSteps() {
        return StepList.startStep(updateItemQuantity)
                .otherSteps(chargeForItems, updateQuantityRecovery, voidPaymentRecovery);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(workflowInput);
    }

    final class UpdateItemQuantity implements Step<FailureRecoveryWorkflowInput> {
        @Override
        public Class<FailureRecoveryWorkflowInput> getInputType() {
            return FailureRecoveryWorkflowInput.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .onExecuteFailureProceedTo(updateQuantityRecovery)
                    .executeRetry(RetryPolicy.newBuilder().maximumAttempts(5).build())
                    .build();
        }

        @Override
        public StepDecision execute(
                final Context context,
                final FailureRecoveryWorkflowInput input) {
            workflowInput.set(context, input);
            database.reduceQuantity(input.itemName, input.requestedQuantity);
            return StepDecision.goTo(chargeForItems, input.requestedQuantity);
        }
    }

    final class ChargeForItems implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .onExecuteFailureProceedTo(voidPaymentRecovery)
                    .executeRetry(RetryPolicy.newBuilder().maximumAttempts(5).build())
                    .build();
        }

        @Override
        public StepDecision execute(final Context context, final Integer quantityRequested) {
            final FailureRecoveryWorkflowInput input = workflowInput.get(context);
            final double itemValue = database.getItemPrice(input.itemName);
            final double orderValue = input.requestedQuantity * itemValue;
            paymentProcessor.processPayment(orderValue);
            return StepDecision.gracefulComplete();
        }
    }

    final class UpdateQuantityRecovery implements Step<FailureRecoveryWorkflowInput> {
        @Override
        public Class<FailureRecoveryWorkflowInput> getInputType() {
            return FailureRecoveryWorkflowInput.class;
        }

        @Override
        public StepDecision execute(
                final Context context,
                final FailureRecoveryWorkflowInput input) {
            database.increaseQuantity(input.itemName, input.requestedQuantity);
            return StepDecision.forceFail("Failed to process transaction");
        }
    }

    final class VoidPaymentRecovery implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            final FailureRecoveryWorkflowInput workflow =
                    workflowInput.get(context);
            final double itemValue = database.getItemPrice(workflow.itemName);
            final double orderValue = workflow.requestedQuantity * itemValue;
            paymentProcessor.voidPayment(orderValue);
            return StepDecision.goTo(updateQuantityRecovery, workflow);
        }
    }
}

class DatabaseConnection {
    private static final Random RANDOM = new Random();

    public void reduceQuantity(final String itemName, final int quantity) {
        System.out.println("Reducing quantity: " + quantity);
        if (quantity > RANDOM.nextInt(10)) {
            throw new RuntimeException("not enough items available");
        }
    }

    public void increaseQuantity(final String itemName, final int quantity) {
        System.out.println("Increasing quantity: " + quantity);
    }

    public double getItemPrice(final String itemName) {
        return 3.14;
    }
}

class PaymentProcessor {
    public void processPayment(final double price) {
        throw new RuntimeException("Payment could not be processed");
    }

    public void voidPayment(final double price) {
        System.out.printf("Voiding payment for $ %.2f%n", price);
    }
}
