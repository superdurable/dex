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

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Client;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.exceptions.FlowNotActiveException;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.util.Random;

@Component
public class ChildFlow implements Flow<String> {
    public static final String PARENT_WORKFLOW_ID = "ParentWorkflowId";

    public final Attribute<String> parentWorkflowId =
            Attribute.define(PARENT_WORKFLOW_ID, String.class);

    private final ObjectProvider<Client> clientProvider;
    private final Processing processing = new Processing();

    public ChildFlow(final ObjectProvider<Client> clientProvider) {
        this.clientProvider = clientProvider;
    }

    private Client client() {
        return clientProvider.getObject();
    }

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(processing);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(parentWorkflowId);
    }

    final class Processing implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            final int random = new Random().nextInt(60);
            return Wait.anyOf(Timer.byDuration(Duration.ofSeconds(random)));
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            final String parentId = parentWorkflowId.get(context);
            if (parentId != null) {
                final ParentFlow stub = client().newRpcStub(ParentFlow.class, parentId);
                try {
                    client().invokeRPC(stub::completeChildWorkflow, context.getFlowId());
                } catch (final FlowNotActiveException inactive) {
                    System.out.println(
                            "Parent workflow may have completed, might be duplicate "
                                    + "completion request, ignore it.");
                }
            }
            return StepDecision.gracefulComplete();
        }
    }
}
