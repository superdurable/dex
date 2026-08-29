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

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeLock;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.SubFlow;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.List;

@Component
public class AdvancedShortLiveParentFlow implements Flow<ParentInput> {
    public static final int DEFAULT_CONCURRENCY = 10;
    public static final int MAX_BUFFERED_REQUESTS = 100;

    public final Channel<String> requestChannel = Channel.define("RequestChannel", String.class);
    public final Attribute<Integer> currSubFlowNum = Attribute.define("CurrSubFlowNum", Integer.class);

    private final InitStep initStep = new InitStep();
    private final HandleRequestStep handleRequestStep = new HandleRequestStep();
    private final HandleSubFlowStep handleSubFlowStep;

    public AdvancedShortLiveParentFlow(final ExampleSubFlow exampleSubFlow) {
        handleSubFlowStep = new HandleSubFlowStep(exampleSubFlow);
    }

    @Override
    public StepList<ParentInput> getSteps() {
        return StepList.startStep(initStep).otherSteps(handleRequestStep, handleSubFlowStep);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(requestChannel, currSubFlowNum);
    }

    @RPC
    public RPCResult<Boolean> sendRequest(final Context context, final String request) {
        if (requestChannel.size(context) >= MAX_BUFFERED_REQUESTS) {
            return RPCResult.of(false);
        }
        requestChannel.publish(context, request);
        return RPCResult.of(true);
    }

    final class InitStep implements Step<ParentInput> {
        @Override
        public Class<ParentInput> getInputType() {
            return ParentInput.class;
        }

        @Override
        public StepDecision execute(final Context context, final ParentInput input) {
            for (final String request : input.requests) {
                requestChannel.publish(context, request);
            }
            currSubFlowNum.set(context, 0);
            final int concurrency = input.concurrency > 0 ? input.concurrency : DEFAULT_CONCURRENCY;
            final StepMovement<?>[] movements = new StepMovement<?>[concurrency];
            for (int index = 0; index < concurrency; index++) {
                movements[index] = StepMovement.of(HandleRequestStep.class, null);
            }
            return StepDecision.goToMany(movements);
        }
    }

    final class HandleRequestStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder().addExecuteLock(AttributeLock.of(currSubFlowNum)).build();
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(requestChannel.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final List<String> requests = requestChannel.getConditionResults(context);
            currSubFlowNum.set(context, currSubFlowNum.get(context) + 1);
            return StepDecision.goTo(HandleSubFlowStep.class, requests.get(0));
        }
    }

    final class HandleSubFlowStep implements Step<String> {
        private final ExampleSubFlow exampleSubFlow;

        HandleSubFlowStep(final ExampleSubFlow exampleSubFlow) {
            this.exampleSubFlow = exampleSubFlow;
        }

        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder().addExecuteLock(AttributeLock.of(currSubFlowNum)).build();
        }

        @Override
        public Wait waitFor(final Context context, final String request) {
            return Wait.until(SubFlow.run(ExampleSubFlow.class, request));
        }

        @Override
        public StepDecision execute(final Context context, final String request) {
            final int current = currSubFlowNum.get(context) - 1;
            currSubFlowNum.set(context, current);
            if (current == 0) {
                return StepDecision.forceCompleteIfChannelsEmpty(
                        null,
                        StepMovement.of(HandleRequestStep.class, null),
                        requestChannel);
            }
            return StepDecision.goTo(HandleRequestStep.class, null);
        }
    }
}
