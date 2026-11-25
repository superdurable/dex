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

import io.superdurable.dex.Client;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.StepCompletion;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.WaitForStepCompletionOptions;
import io.superdurable.dex.exceptions.WorkerInvocationException;
import io.superdurable.dex.patterns.controllongrunningwork.RecordBackfillFlow;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Asserts the Control Long-Running Work pattern example. */
@ExtendWith(SharedIntegExtension.class)
public class ControlLongRunningWorkIntegTest {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(45);

    /** At the default batch size the backfill needs 100 seconds, longer than the wait timeout. */
    @Test
    void readsTheBatchSizeEachBatch() {
        final String flowId = startRecordBackfill("control-long-running-work-batch");
        final RecordBackfillFlow stub = backfillStub(flowId);

        for (final int invalidSize : new int[] {0, 1001}) {
            final WorkerInvocationException rejected = assertThrows(
                    WorkerInvocationException.class,
                    () -> client().invokeRPC(stub::setBatchSize, invalidSize));
            assertTrue(
                    rejected.getMessage().contains("batch size must be between 1 and 1000"),
                    rejected.getMessage());
        }
        // The first batch has used the default size, so a later batch must read the new one.
        waitForFirstRecordBatch(flowId);
        client().invokeRPC(stub::setBatchSize, 1000);

        assertEquals("processed 1000 records", completedOutput(flowId));
    }

    @Test
    void stopsBeforeTheNextBatch() {
        final String flowId = startRecordBackfill("control-long-running-work-stop");

        waitForFirstRecordBatch(flowId);
        client().invokeRPC(backfillStub(flowId)::requestStop);

        final String output = completedOutput(flowId);
        final String prefix = "stopped before record ";
        assertTrue(output.matches(prefix + "\\d+"), output);
        assertTrue(Integer.parseInt(output.substring(prefix.length())) >= 11, output);
    }

    private static void waitForFirstRecordBatch(final String flowId) {
        client().waitForStepCompletion(
                flowId,
                StepExecutionId.of("ProcessRecordBatch", 1),
                WaitForStepCompletionOptions.newBuilder().requestTimeout(WAIT_TIMEOUT).build());
    }

    private static String startRecordBackfill(final String prefix) {
        final String flowId = environment().newFlowId("pattern-" + prefix);
        client().startFlow(
                environment().workflowPatternFlow(RecordBackfillFlow.class),
                flowId,
                null,
                environment().startOptions());
        return flowId;
    }

    private static String completedOutput(final String flowId) {
        final FlowResult result = client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.COMPLETED, result.getStatus(), flowId + ": " + result.getErrorMessage());
        final List<String> outputs = result.getCompletions().stream()
                .map((StepCompletion completion) -> completion.getOutput(String.class))
                .toList();
        assertEquals(1, outputs.size(), outputs.toString());
        return outputs.get(0);
    }

    private static RecordBackfillFlow backfillStub(final String flowId) {
        return client().newRpcStub(RecordBackfillFlow.class, flowId);
    }

    private static IntegEnvironment environment() {
        return SharedIntegExtension.environment();
    }

    private static Client client() {
        return environment().client();
    }
}
