/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package io.superdurable.dex.patterns.parallelsubflows;

import io.superdurable.dex.Channel;
import io.superdurable.dex.Client;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.SubFlow;
import io.superdurable.dex.Wait;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.stereotype.Component;

@Component
public final class WaitForHalfParentFlow implements Flow<String[]> {
    public final Channel<Boolean> subFlowCompletedCh =
            Channel.define("SubFlowCompletedCh", Boolean.class);
    public final Channel<Boolean> allDoneCh = Channel.define("AllDoneCh", Boolean.class);

    private final InitStep initStep = new InitStep();
    private final SubFlowStep subFlowStep;
    private final WaitSubFlowsStep waitSubFlowsStep = new WaitSubFlowsStep();

    public WaitForHalfParentFlow(final ObjectProvider<Client> clientProvider) {
        subFlowStep = new SubFlowStep(clientProvider);
    }

    @Override
    public StepList<String[]> getSteps() {
        return StepList.startStep(initStep).otherSteps(subFlowStep, waitSubFlowsStep);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(subFlowCompletedCh, allDoneCh);
    }

    final class InitStep implements Step<String[]> {
        @Override
        public Class<String[]> getInputType() {
            return String[].class;
        }

        @Override
        public StepDecision execute(final Context context, final String[] requests) {
            if (requests.length == 0) {
                return StepDecision.gracefulComplete(null);
            }
            final StepMovement<?>[] movements = new StepMovement<?>[requests.length + 1];
            movements[0] = StepMovement.of(WaitSubFlowsStep.class, requests.length);
            for (int index = 0; index < requests.length; index++) {
                movements[index + 1] = StepMovement.of(SubFlowStep.class, requests[index]);
            }
            return StepDecision.goToMany(movements);
        }
    }

    final class SubFlowStep implements Step<String> {
        private final ObjectProvider<Client> clientProvider;
        SubFlowStep(final ObjectProvider<Client> clientProvider) {
            this.clientProvider = clientProvider;
        }

        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String request) {
            return Wait.anyOf(SubFlow.run(ExampleSubFlow.class, request), allDoneCh.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final String request) {
            final FlowResult result = SubFlow.getConditionResults(context);
            if (result.getStatus() != FlowStatus.RUNNING) {
                subFlowCompletedCh.publish(context, true);
                return StepDecision.gracefulComplete(null);
            }
            clientProvider.getObject().stopFlow(SubFlow.getFlowId(context));
            return StepDecision.gracefulComplete(null);
        }
    }

    final class WaitSubFlowsStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer total) {
            return Wait.until(subFlowCompletedCh.forN((total + 1) / 2));
        }

        @Override
        public StepDecision execute(final Context context, final Integer total) {
            final int remaining = total - (total + 1) / 2;
            for (int index = 0; index < remaining; index++) {
                allDoneCh.publish(context, true);
            }
            return StepDecision.gracefulComplete(null);
        }
    }
}
