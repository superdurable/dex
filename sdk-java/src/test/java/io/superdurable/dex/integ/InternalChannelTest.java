/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.core.Client;
import io.superdurable.dex.core.ClientOptions;
import io.superdurable.dex.integ.internalchannel.BasicInternalChannelWorkflow;
import io.superdurable.dex.integ.internalchannel.WaitingInternalChannelWorkflow;
import io.superdurable.dex.spring.TestSingletonWorkerService;
import io.superdurable.dex.spring.controller.WorkflowRegistry;
import org.junit.jupiter.api.Assertions;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.Arrays;
import java.util.concurrent.ExecutionException;

public class InternalChannelTest {

    @BeforeEach
    public void setup() throws ExecutionException, InterruptedException {
        TestSingletonWorkerService.startWorkerIfNotUp();
    }

    @Test
    public void testBasicInternalWorkflow() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "basic-internal-test-id" + System.currentTimeMillis() / 1000;
        final Integer input = 1;
        final String runId = client.startWorkflow(
                BasicInternalChannelWorkflow.class, wfId, 10, input);
        final Integer output = client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        Assertions.assertEquals(3, output);
    }

    @Test
    public void testWaitingInternalWorkflow() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "waiting-internal-test-id" + System.currentTimeMillis() / 1000;
        final Integer input = 1;
        final String runId = client.startWorkflow(
                WaitingInternalChannelWorkflow.class, wfId, 10, input);
        client.publishToInternalChannelBatch(WaitingInternalChannelWorkflow.class, wfId,  runId,  WaitingInternalChannelWorkflow.INTER_STATE_CHANNEL_NAME, Arrays.asList(2, 3));
        final Integer output = client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        Assertions.assertEquals(6, output);
    }
}
