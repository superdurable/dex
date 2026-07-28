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

import com.google.common.collect.ImmutableMap;
import io.superdurable.dex.core.Client;
import io.superdurable.dex.core.ClientOptions;
import io.superdurable.dex.core.ImmutableStopWorkflowOptions;
import io.superdurable.dex.gen.models.WorkflowStopType;
import io.superdurable.dex.integ.persistence.BasicPersistenceWorkflow;
import io.superdurable.dex.integ.rpc.RpcMemoWorkflow;
import io.superdurable.dex.integ.rpc.RpcWorkflowState2;
import io.superdurable.dex.spring.TestSingletonWorkerService;
import io.superdurable.dex.spring.controller.WorkflowRegistry;
import org.junit.jupiter.api.Assertions;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.Arrays;
import java.util.Map;
import java.util.concurrent.ExecutionException;

import static io.superdurable.dex.integ.persistence.BasicPersistenceWorkflow.TEST_SEARCH_ATTRIBUTE_INT;
import static io.superdurable.dex.integ.persistence.BasicPersistenceWorkflow.TEST_SEARCH_ATTRIBUTE_KEYWORD;
import static io.superdurable.dex.integ.rpc.RpcMemoWorkflow.TEST_DATA_OBJECT_KEY;

public class RpcWithMemoTest {

    private static final String RPC_INPUT = "rpc-input";

    public static final Long RPC_OUTPUT = 100L;
    public static final String HARDCODED_STR = "random-string";

    @BeforeEach
    public void setup() throws ExecutionException, InterruptedException {
        TestSingletonWorkerService.startWorkerIfNotUp();
    }

    @Test
    public void testRpcMemoWorkflowFunc1() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testRpcMemoWorkflowFunc1" + System.currentTimeMillis() / 1000;
        final String runId = client.startWorkflow(
                RpcMemoWorkflow.class, wfId, 10, 999);

        final RpcMemoWorkflow rpcStub = client.newRpcStub(RpcMemoWorkflow.class, wfId, "");

        String value;
        client.invokeRPC(rpcStub::testRpcSetDataAttribute, "test-value");
        value = client.invokeRPC(rpcStub::testRpcGetDataAttributeStrongConsistent);
        Assertions.assertEquals("test-value", value);
        Thread.sleep(100);
        value = client.invokeRPC(rpcStub::testRpcGetDataAttribute);
        Assertions.assertEquals("test-value", value);

        client.invokeRPC(rpcStub::testRpcSetDataAttribute, null);
        value = client.invokeRPC(rpcStub::testRpcGetDataAttributeStrongConsistent);
        Assertions.assertNull(value);
        Thread.sleep(100);
        value = client.invokeRPC(rpcStub::testRpcGetDataAttribute);
        Assertions.assertNull(value);

        client.invokeRPC(rpcStub::testRpcSetKeyword, "test-value");
        value = client.invokeRPC(rpcStub::testRpcGetKeywordStrongConsistency);
        Assertions.assertEquals("test-value", value);
        Thread.sleep(100);
        value = client.invokeRPC(rpcStub::testRpcGetKeyword);
        Assertions.assertEquals("test-value", value);

        client.invokeRPC(rpcStub::testRpcSetKeyword, null);
        value = client.invokeRPC(rpcStub::testRpcGetKeywordStrongConsistency);
        Assertions.assertNull(value);
        Thread.sleep(100);
        value = client.invokeRPC(rpcStub::testRpcGetKeyword);
        Assertions.assertTrue(value == null || value.isEmpty());

        final Long rpcOutput = client.invokeRPC(rpcStub::testRpcFunc1, RPC_INPUT);

        Assertions.assertEquals(RPC_OUTPUT, rpcOutput);

        // output
        final Integer output = client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        RpcWorkflowState2.resetCounter();
        Assertions.assertEquals(2, output);

        // data attrs
        Map<String, Object> dataAttrs =
                client.getWorkflowDataAttributes(BasicPersistenceWorkflow.class, wfId, runId, Arrays.asList(BasicPersistenceWorkflow.TEST_DATA_OBJECT_KEY));
        Assertions.assertEquals(
                ImmutableMap.builder()
                        .put(TEST_DATA_OBJECT_KEY, RPC_INPUT)
                        .build(), dataAttrs);

        // search attrs
        final Map<String, Object> searchAttributes = client.getWorkflowSearchAttributes(BasicPersistenceWorkflow.class,
                wfId, "", Arrays.asList(TEST_SEARCH_ATTRIBUTE_KEYWORD, TEST_SEARCH_ATTRIBUTE_INT));

        Assertions.assertEquals(ImmutableMap.builder()
                .put(TEST_SEARCH_ATTRIBUTE_INT, RPC_OUTPUT)
                .put(TEST_SEARCH_ATTRIBUTE_KEYWORD, RPC_INPUT)
                .build(), searchAttributes);
    }

    @Test
    public void testRpcMemoWorkflowFunc0() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testRpcMemoWorkflowFunc0" + System.currentTimeMillis() / 1000;
        final String runId = client.startWorkflow(
                RpcMemoWorkflow.class, wfId, 10, 999);

        final RpcMemoWorkflow rpcStub = client.newRpcStub(RpcMemoWorkflow.class, wfId, "");
        final Long rpcOutput = client.invokeRPC(rpcStub::testRpcFunc0);

        Assertions.assertEquals(RPC_OUTPUT, rpcOutput);

        // output
        final Integer output = client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        RpcWorkflowState2.resetCounter();
        Assertions.assertEquals(2, output);

        // data attrs
        Map<String, Object> dataAttrs =
                client.getWorkflowDataAttributes(BasicPersistenceWorkflow.class, wfId, runId, Arrays.asList(BasicPersistenceWorkflow.TEST_DATA_OBJECT_KEY));
        Assertions.assertEquals(
                ImmutableMap.builder()
                        .put(TEST_DATA_OBJECT_KEY, HARDCODED_STR)
                        .build(), dataAttrs);

        // search attrs
        final Map<String, Object> searchAttributes = client.getWorkflowSearchAttributes(BasicPersistenceWorkflow.class,
                wfId, "", Arrays.asList(TEST_SEARCH_ATTRIBUTE_KEYWORD, TEST_SEARCH_ATTRIBUTE_INT));

        Assertions.assertEquals(ImmutableMap.builder()
                .put(TEST_SEARCH_ATTRIBUTE_INT, RPC_OUTPUT)
                .put(TEST_SEARCH_ATTRIBUTE_KEYWORD, HARDCODED_STR)
                .build(), searchAttributes);

    }

    @Test
    public void testRpcMemoWorkflowProc1() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testRpcMemoWorkflowProc1" + System.currentTimeMillis() / 1000;
        final String runId = client.startWorkflow(
                RpcMemoWorkflow.class, wfId, 10, 999);

        final RpcMemoWorkflow rpcStub = client.newRpcStub(RpcMemoWorkflow.class, wfId, "");
        client.invokeRPC(rpcStub::testRpcProc1, RPC_INPUT);

        // output
        final Integer output = client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        RpcWorkflowState2.resetCounter();
        Assertions.assertEquals(2, output);

        // data attrs
        Map<String, Object> dataAttrs =
                client.getWorkflowDataAttributes(BasicPersistenceWorkflow.class, wfId, runId, Arrays.asList(BasicPersistenceWorkflow.TEST_DATA_OBJECT_KEY));
        Assertions.assertEquals(
                ImmutableMap.builder()
                        .put(TEST_DATA_OBJECT_KEY, RPC_INPUT)
                        .build(), dataAttrs);

        // search attrs
        final Map<String, Object> searchAttributes = client.getWorkflowSearchAttributes(BasicPersistenceWorkflow.class,
                wfId, "", Arrays.asList(TEST_SEARCH_ATTRIBUTE_KEYWORD, TEST_SEARCH_ATTRIBUTE_INT));

        Assertions.assertEquals(ImmutableMap.builder()
                .put(TEST_SEARCH_ATTRIBUTE_INT, RPC_OUTPUT)
                .put(TEST_SEARCH_ATTRIBUTE_KEYWORD, RPC_INPUT)
                .build(), searchAttributes);
    }

    @Test
    public void testRpcMemoWorkflowProc0() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testRpcMemoWorkflowProc0" + System.currentTimeMillis() / 1000;
        final String runId = client.startWorkflow(
                RpcMemoWorkflow.class, wfId, 10, 999);

        final RpcMemoWorkflow rpcStub = client.newRpcStub(RpcMemoWorkflow.class, wfId, "");
        client.invokeRPC(rpcStub::testRpcProc0);

        // output
        final Integer output = client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        RpcWorkflowState2.resetCounter();
        Assertions.assertEquals(2, output);

        // data attrs
        Map<String, Object> dataAttrs =
                client.getWorkflowDataAttributes(BasicPersistenceWorkflow.class, wfId, runId, Arrays.asList(BasicPersistenceWorkflow.TEST_DATA_OBJECT_KEY));
        Assertions.assertEquals(
                ImmutableMap.builder()
                        .put(TEST_DATA_OBJECT_KEY, HARDCODED_STR)
                        .build(), dataAttrs);

        // search attrs
        final Map<String, Object> searchAttributes = client.getWorkflowSearchAttributes(BasicPersistenceWorkflow.class,
                wfId, "", Arrays.asList(TEST_SEARCH_ATTRIBUTE_KEYWORD, TEST_SEARCH_ATTRIBUTE_INT));

        Assertions.assertEquals(ImmutableMap.builder()
                .put(TEST_SEARCH_ATTRIBUTE_INT, RPC_OUTPUT)
                .put(TEST_SEARCH_ATTRIBUTE_KEYWORD, HARDCODED_STR)
                .build(), searchAttributes);
    }

    @Test
    public void testRpcMemoWorkflowFunc1ReadOnly() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testRpcMemoWorkflowFunc1ReadOnly" + System.currentTimeMillis() / 1000;
        final String runId = client.startWorkflow(
                RpcMemoWorkflow.class, wfId, 10, 999);

        final RpcMemoWorkflow rpcStub = client.newRpcStub(RpcMemoWorkflow.class, wfId, "");
        final Long rpcOutput = client.invokeRPC(rpcStub::testRpcFunc1Readonly, RPC_INPUT);

        Assertions.assertEquals(RPC_OUTPUT, rpcOutput);

        client.stopWorkflow(wfId, "", ImmutableStopWorkflowOptions.builder()
                .workflowStopType(WorkflowStopType.FAIL)
                .reason(HARDCODED_STR)
                .build());

    }

}
