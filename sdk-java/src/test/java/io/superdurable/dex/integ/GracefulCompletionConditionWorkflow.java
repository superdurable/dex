/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Sustainable Use License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.GracefulCompletion;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Wait;

import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;

class GracefulCompletionConditionWorkflow implements Flow<Boolean> {
    final Channel<String> messages = Channel.define("messages", String.class);
    final Channel<Void> gate = Channel.define("gate", Void.class);
    final List<GracefulCompletionConditionObservation> observations =
            new CopyOnWriteArrayList<GracefulCompletionConditionObservation>();
    final List<Boolean> supportObservations = new CopyOnWriteArrayList<Boolean>();
    private final GracefulCompletionConditionGateStep gateStep =
            new GracefulCompletionConditionGateStep();
    private final GracefulCompletionConditionWaitStep waitStep =
            new GracefulCompletionConditionWaitStep();

    @Override
    public StepList<Boolean> getSteps() {
        return StepList.startStep(gateStep).otherSteps(waitStep);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(messages, gate);
    }

    @RPC
    public void publishMessage(final Context context, final String message) {
        messages.publish(context, message);
    }

    @RPC
    public void openGate(final Context context) {
        gate.publish(context, null);
    }

    final class GracefulCompletionConditionGateStep implements Step<Boolean> {
        @Override
        public Class<Boolean> getInputType() {
            return Boolean.class;
        }

        @Override
        public Wait waitFor(final Context context, final Boolean shouldWaitForGate) {
            if (shouldWaitForGate) {
                return Wait.until(gate.forOne());
            }
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final Boolean shouldWaitForGate) {
            return StepDecision.goTo(GracefulCompletionConditionWaitStep.class, shouldWaitForGate);
        }
    }

    final class GracefulCompletionConditionWaitStep implements Step<Boolean> {
        @Override
        public Class<Boolean> getInputType() {
            return Boolean.class;
        }

        @Override
        public Wait waitFor(final Context context, final Boolean input) {
            final boolean isSupported = context.supportsGracefulCompletionCondition();
            supportObservations.add(isSupported);
            if (!isSupported) {
                return Wait.until(messages.forOne());
            }
            return Wait.anyOf(messages.forOne(), GracefulCompletion.requested());
        }

        @Override
        public StepDecision execute(final Context context, final Boolean input) {
            final List<String> consumed = messages.getConditionResults(context);
            final boolean isGracefulCompleted = context.isGracefulCompletionConditionCompleted();
            observations.add(new GracefulCompletionConditionObservation(consumed, isGracefulCompleted));
            if (!consumed.isEmpty()) {
                return StepDecision.goTo(GracefulCompletionConditionWaitStep.class, input);
            }
            if (isGracefulCompleted) {
                return StepDecision.deadEnd();
            }
            return StepDecision.goTo(GracefulCompletionConditionWaitStep.class, input);
        }
    }
}

final class GracefulCompletionConditionObservation {
    final List<String> consumedMessages;
    final boolean isGracefulCompleted;

    GracefulCompletionConditionObservation(
            final List<String> consumedMessages,
            final boolean isGracefulCompleted) {
        this.consumedMessages = consumedMessages;
        this.isGracefulCompleted = isGracefulCompleted;
    }
}
