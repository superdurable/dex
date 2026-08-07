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

package io.superdurable.dex.patterns.workflow.scalableparallel;

import io.superdurable.dex.Client;
import io.superdurable.dex.Context;
import io.superdurable.dex.DexException;
import io.superdurable.dex.ErrorSubStatus;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.controller.ExampleFlows;
import io.superdurable.dex.patterns.workflow.scalableparallel.exceptions.EnqueueFailedException;
import io.superdurable.dex.patterns.workflow.scalableparallel.models.BatchEnqueueRequest;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.stereotype.Component;

import java.util.ArrayList;
import java.util.List;
import java.util.Random;
import java.util.UUID;

@Component
public class RequestReceiverFlow implements Flow<Integer> {
    private final ObjectProvider<Client> clientProvider;
    private final ParentFlow parentFlow;
    private final Request request = new Request();

    public RequestReceiverFlow(
            final ObjectProvider<Client> clientProvider, final ParentFlow parentFlow) {
        this.clientProvider = clientProvider;
        this.parentFlow = parentFlow;
    }

    private Client client() {
        return clientProvider.getObject();
    }

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(request);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    final class Request implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer numberOfChildWfs) {
            final BatchEnqueueRequest batch = generateTasks(numberOfChildWfs);
            final int randSuffix = new Random().nextInt(ParentFlow.NUM_PARENT_WORKFLOWS) + 1;
            final String parentWorkflowId = "parent_workflow_" + randSuffix;

            final ParentFlow stub = client().newRpcStub(ParentFlow.class, parentWorkflowId);
            try {
                final boolean success = client().invokeRPC(stub::enqueue, batch);
                if (!success) {
                    throw new EnqueueFailedException("Enqueue failed, retry in next attempt");
                }
            } catch (final DexException e) {
                if (e.getSubStatus() == ErrorSubStatus.FLOW_NOT_EXISTS) {
                    client().startFlow(
                            parentFlow,
                            parentWorkflowId,
                            batch,
                            ExampleFlows.startOptions());
                } else {
                    throw e;
                }
            }

            return StepDecision.gracefulComplete();
        }

        private BatchEnqueueRequest generateTasks(final Integer numberOfChildWfs) {
            final List<String> uuids = new ArrayList<String>();
            for (int i = 0; i < numberOfChildWfs; i++) {
                uuids.add(UUID.randomUUID().toString());
            }
            return new BatchEnqueueRequest(uuids);
        }
    }
}
