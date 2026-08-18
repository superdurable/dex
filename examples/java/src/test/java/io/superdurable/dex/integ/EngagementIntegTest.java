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

import io.superdurable.dex.products.engagement.EngagementDescription;
import io.superdurable.dex.products.engagement.EngagementFlow;
import io.superdurable.dex.products.engagement.EngagementInput;
import io.superdurable.dex.products.engagement.Status;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;

@ExtendWith(SharedIntegExtension.class)
public class EngagementIntegTest {
    @Test
    void engagementStartChannelRpcAndStatus() throws Exception {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final EngagementFlow flow = environment.engagementFlow();
        final String flowId = environment.newFlowId("engagement");
        final EngagementInput input = new EngagementInput(
                "employer-ci",
                "job-seeker-ci",
                "created");

        final String runId = environment.client().startFlow(
                flow,
                flowId,
                input,
                environment.startOptions());
        assertNotNull(runId);
        assertFalse(runId.isEmpty());

        environment.awaitAttribute(
                flowId,
                flow.engagementStatus,
                Status.INITIATED,
                Duration.ofSeconds(20));

        final EngagementFlow stub = environment.client().newRpcStub(EngagementFlow.class, flowId);
        final EngagementDescription description = environment.client().invokeRPC(stub::describe);
        assertEquals(Status.INITIATED, description.currentStatus);

        environment.client().publish(flowId, flow.optOutReminder, (Void) null);

        final Status declined = environment.client().invokeRPC(
                stub::decline,
                "declined in integration test");
        assertEquals(Status.DECLINED, declined);

        final Status accepted = environment.client().invokeRPC(
                stub::accept,
                "accepted in integration test");
        assertEquals(Status.ACCEPTED, accepted);

        environment.awaitAttribute(
                flowId,
                flow.engagementStatus,
                Status.ACCEPTED,
                Duration.ofSeconds(20));

        final String output = environment.client().waitForFlow(flowId, Duration.ofSeconds(45)).getSingleOutput(String.class);
        assertEquals("done", output);
    }
}
