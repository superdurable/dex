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
import io.superdurable.dex.integ.stateapifail.WorkflowStateFailProceedToRecover;
import io.superdurable.dex.integ.stateapifail.WorkflowStateFailProceedToRecoverNoWaitUntil;
import io.superdurable.dex.spring.TestSingletonWorkerService;
import io.superdurable.dex.spring.controller.WorkflowRegistry;
import org.junit.jupiter.api.Assertions;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.concurrent.ExecutionException;

public class StateRecoveryTest {

    @BeforeEach
    public void setup() throws ExecutionException, InterruptedException {
        TestSingletonWorkerService.startWorkerIfNotUp();
    }

    @Test
    public void testStateApiFailAndRecoveryWorkflow() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final long startTs = System.currentTimeMillis();
        final String wfId = "testStateApiFailAndRecoveryWorkflow" + startTs / 1000;
        final Integer input = 5;

        client.startWorkflow(
                WorkflowStateFailProceedToRecover.class, wfId, 10, input);

        Integer output = client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        Assertions.assertEquals(10, output);
    }

    @Test
    public void testStateApiFailAndRecoveryNoWaitUntilWorkflow() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final long startTs = System.currentTimeMillis();
        final String wfId = "testStateApiFailAndRecoveryNoWaitUntilWorkflow" + startTs / 1000;
        final Integer input = 5;

        client.startWorkflow(
                WorkflowStateFailProceedToRecoverNoWaitUntil.class, wfId, 10, input);

        Integer output = client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        Assertions.assertEquals(10, output);
    }

}
