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
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
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
            final AdvancedShortLiveParentFlow stub =
                    clientProvider.getObject().newRpcStub(AdvancedShortLiveParentFlow.class, parentId);
            if (!clientProvider.getObject().invokeRPC(stub::sendRequest, input.request)) {
                throw new IllegalStateException("parent " + parentId + " rejected the request");
            }
            return StepDecision.gracefulComplete(parentId);
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
