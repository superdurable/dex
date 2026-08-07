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

package io.superdurable.dex.integ;

import io.superdurable.dex.workflow.microservices.OrchestrationFlow;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;

@ExtendWith(SharedIntegExtension.class)
public class MicroserviceIntegTest {
    @Test
    void microserviceStartRpcAndChannel() throws Exception {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final OrchestrationFlow flow = environment.orchestrationFlow();
        final String flowId = environment.newFlowId("microservice");

        final String runId = environment.client().startFlow(
                flow,
                flowId,
                "initial-data",
                environment.startOptions());
        assertNotNull(runId);
        assertFalse(runId.isEmpty());

        environment.awaitAttribute(flowId, flow.data, "initial-data", Duration.ofSeconds(20));

        final OrchestrationFlow stub =
                environment.client().newRpcStub(OrchestrationFlow.class, flowId);
        final String oldData = environment.client().invokeRPC(stub::swap, "updated-data");
        assertEquals("initial-data", oldData);

        environment.client().publish(flowId, flow.ready, (Void) null);

        final String output = environment.client().waitForFlow(
                flowId,
                String.class,
                Duration.ofSeconds(45));
        assertEquals("updated-data", output);
    }
}
