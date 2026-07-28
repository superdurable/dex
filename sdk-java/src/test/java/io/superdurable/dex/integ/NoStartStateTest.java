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

import io.superdurable.dex.core.Client;
import io.superdurable.dex.core.ClientOptions;
import io.superdurable.dex.integ.rpc.DeadEndStateWorkflow;
import io.superdurable.dex.integ.rpc.NoStartStateWorkflow;
import io.superdurable.dex.integ.rpc.NoStateWorkflow;
import io.superdurable.dex.integ.rpc.RpcWorkflowState2;
import io.superdurable.dex.spring.TestSingletonWorkerService;
import io.superdurable.dex.spring.controller.WorkflowRegistry;
import org.junit.jupiter.api.Assertions;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.concurrent.ExecutionException;

public class NoStartStateTest {

    private static final String RPC_INPUT = "rpc-input";

    public static final Long RPC_OUTPUT = 100L;
    public static final String HARDCODED_STR = "random-string";

    @BeforeEach
    public void setup() throws ExecutionException, InterruptedException {
        TestSingletonWorkerService.startWorkerIfNotUp();
    }

    @Test
    public void testNoStartStateWorkflow() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testNoStartStateWorkflow" + System.currentTimeMillis() / 1000;
        client.startWorkflow(
                NoStartStateWorkflow.class, wfId, 10, 999);

        final NoStartStateWorkflow rpcStub = client.newRpcStub(NoStartStateWorkflow.class, wfId, "");
        final Long rpcOutput = client.invokeRPC(rpcStub::testRpcFunc1, RPC_INPUT);

        Assertions.assertEquals(RPC_OUTPUT, rpcOutput);

        // output
        client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        final int counter = RpcWorkflowState2.resetCounter();
        // TODO fix
//        Assertions.assertEquals(1, counter);
    }

    @Test
    public void testNoStateWorkflow() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testNoStateWorkflow" + System.currentTimeMillis() / 1000;
        client.startWorkflow(
                NoStateWorkflow.class, wfId, 10, 999);

        final NoStateWorkflow rpcStub = client.newRpcStub(NoStateWorkflow.class, wfId, "");
        final Long rpcOutput = client.invokeRPC(rpcStub::testRpcFunc1, RPC_INPUT);

        Assertions.assertEquals(RPC_OUTPUT, rpcOutput);

        client.stopWorkflow(wfId, null);
    }

    @Test
    public void testDeadEndWorkflow() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testDeadEndWorkflow" + System.currentTimeMillis() / 1000;
        client.startWorkflow(
                DeadEndStateWorkflow.class, wfId, 10);

        Thread.sleep(2000);
        final DeadEndStateWorkflow rpcStub = client.newRpcStub(DeadEndStateWorkflow.class, wfId, "");
        final Long rpcOutput = client.invokeRPC(rpcStub::testRpcFunc1, RPC_INPUT);
        RpcWorkflowState2.resetCounter();

        Assertions.assertEquals(RPC_OUTPUT, rpcOutput);

        Integer out = client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        Assertions.assertNull(out);
    }

}
