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

import io.superdurable.dex.Client;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.IdReusePolicy;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;
import io.superdurable.dex.exceptions.FlowNotActiveException;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.stereotype.Component;

import java.nio.charset.StandardCharsets;

@Component
public final class SubmitRequestFlow implements Flow<SubmitRequestInput> {
    private final SubmitStep submitStep;

    public SubmitRequestFlow(
            final ObjectProvider<Client> clientProvider,
            final AdvancedShortLiveParentFlow parentFlow) {
        submitStep = new SubmitStep(clientProvider, parentFlow);
    }

    @Override
    public StepList<SubmitRequestInput> getSteps() {
        return StepList.startStep(submitStep);
    }

    static final class SubmitStep implements Step<SubmitRequestInput> {
        private final ObjectProvider<Client> clientProvider;
        private final AdvancedShortLiveParentFlow parentFlow;

        SubmitStep(
                final ObjectProvider<Client> clientProvider,
                final AdvancedShortLiveParentFlow parentFlow) {
            this.clientProvider = clientProvider;
            this.parentFlow = parentFlow;
        }

        @Override
        public Class<SubmitRequestInput> getInputType() {
            return SubmitRequestInput.class;
        }

        @Override
        public StepDecision execute(final Context context, final SubmitRequestInput input) {
            if (input.parentIds.length == 0) {
                throw new IllegalArgumentException("at least one parent Flow ID is required");
            }
            final String parentId = input.parentIds[partition(input.request, input.parentIds.length)];
            if (!enqueueRequest(parentId, input.request)) {
                throw new IllegalStateException("parent " + parentId + " rejected the request");
            }
            return StepDecision.gracefulComplete(parentId);
        }

        private boolean enqueueRequest(final String parentId, final String request) {
            final Client client = clientProvider.getObject();
            final AdvancedShortLiveParentFlow stub =
                    client.newRpcStub(AdvancedShortLiveParentFlow.class, parentId);
            try {
                return client.invokeRPC(stub::sendRequest, request);
            } catch (final FlowNotActiveException inactive) {
                try {
                    client.startFlow(
                            parentFlow,
                            parentId,
                            new ParentInput(
                                    new String[] {request},
                                    AdvancedShortLiveParentFlow.DEFAULT_CONCURRENCY),
                            StartFlowOptions.newBuilder()
                                    .idReusePolicy(IdReusePolicy.ALLOW_IF_NOT_RUNNING)
                                    .build());
                    return true;
                } catch (final FlowAlreadyStartedException alreadyStarted) {
                    return client.invokeRPC(stub::sendRequest, request);
                }
            }
        }

        private static int partition(final String request, final int partitions) {
            int hash = 0x811c9dc5;
            for (final byte value : request.getBytes(StandardCharsets.UTF_8)) {
                hash ^= Byte.toUnsignedInt(value);
                hash *= 0x01000193;
            }
            return Integer.remainderUnsigned(hash, partitions);
        }
    }
}
