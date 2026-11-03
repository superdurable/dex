/*
 * Copyright (c) 2026 Super Durable, Inc.
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

import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.StreamMessage;
import io.superdurable.dex.primitives.rpccancelingsteps.ReportGenerationFlow;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

@ExtendWith(SharedIntegExtension.class)
public class RpcCancelingStepsIntegTest {
    @Test
    void stopReportCancelsRunningGeneration() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final String flowId = environment.newFlowId("rpc-canceling-steps");
        environment.client().startFlow(
                environment.reportGenerationFlow(),
                flowId,
                "q3",
                environment.startOptions());

        final StreamMessage<Integer> firstSection = environment.client().readStream(
                flowId,
                ReportGenerationFlow.reportProgress,
                "",
                Duration.ofSeconds(20));
        assertEquals(1, firstSection.getValue());

        final ReportGenerationFlow stub =
                environment.client().newRpcStub(ReportGenerationFlow.class, flowId);
        assertTrue(environment.client().invokeRPC(stub::stopReport));

        final FlowResult result = environment.client().waitForFlow(flowId, Duration.ofSeconds(45));
        assertEquals(FlowStatus.COMPLETED, result.getStatus());
        assertEquals(
                ReportGenerationFlow.REPORT_STOPPED,
                result.getSingleOutput(String.class));
    }
}
