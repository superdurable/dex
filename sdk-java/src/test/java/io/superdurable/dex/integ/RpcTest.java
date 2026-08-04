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

import com.google.common.collect.ImmutableMap;
import io.superdurable.dex.core.Client;
import io.superdurable.dex.core.ClientOptions;
import io.superdurable.dex.core.ClientSideException;
import io.superdurable.dex.core.ImmutableStopWorkflowOptions;
import io.superdurable.dex.core.ImmutableWorkflowOptions;
import io.superdurable.dex.gen.models.*;
import io.superdurable.dex.integ.persistence.BasicPersistenceWorkflow;
import io.superdurable.dex.integ.rpc.DeadEndStateWorkflow;
import io.superdurable.dex.integ.rpc.NoStateWorkflow;
import io.superdurable.dex.integ.rpc.RpcWorkflow;
import io.superdurable.dex.integ.rpc.RpcWorkflowState2;
import io.superdurable.dex.spring.TestSingletonWorkerService;
import io.superdurable.dex.spring.controller.WorkflowRegistry;
import org.junit.jupiter.api.Assertions;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.Map;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;

import static io.superdurable.dex.integ.persistence.BasicPersistenceWorkflow.TEST_SEARCH_ATTRIBUTE_INT;
import static io.superdurable.dex.integ.persistence.BasicPersistenceWorkflow.TEST_SEARCH_ATTRIBUTE_KEYWORD;
import static io.superdurable.dex.integ.rpc.RpcWorkflow.TEST_DATA_OBJECT_KEY;

public class RpcTest {

    private static final String RPC_INPUT = "rpc-input";

    public static final Long RPC_OUTPUT = 100L;
    public static final String HARDCODED_STR = "random-string";

    @BeforeEach
    public void setup() throws ExecutionException, InterruptedException {
        TestSingletonWorkerService.startWorkerIfNotUp();
    }

    @Test
    public void testRPCLocking() throws InterruptedException, ExecutionException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testRPCLocking" + System.currentTimeMillis() / 1000;
        client.startWorkflow(
                NoStateWorkflow.class, wfId, 1000, 999,
                ImmutableWorkflowOptions.builder()
                        .workflowConfigOverride(
                                new WorkflowConfig()
                                        .continueAsNewThreshold(2)
                        )
                        .build());

        final NoStateWorkflow rpcStub = client.newRpcStub(NoStateWorkflow.class, wfId, "");

        ExecutorService executor = Executors.newFixedThreadPool(10);
        final ArrayList<Future<String>> futures = new ArrayList<>();
        int total = 100;
        for (int i = 0; i < total; i++) {

            final Future<String> future = executor.submit(() -> {
                        try {
                            return client.invokeRPC(rpcStub::increaseCounter);
                        } catch (ClientSideException e) {
                            if (e.getStatusCode() != 450) {
                                throw e;
                            }
                        }
                        return "fail";
                    }
            );
            futures.add(future);
        }

        int succ = 0;
        for (int i = 0; i < total; i++) {
            ;
            try {
                final String done = futures.get(i).get();
                if (done.equals("done")) {
                    succ++;
                }
            } catch (Exception ignored) {
            }
        }

        Assertions.assertTrue(succ > 0);
        Assertions.assertEquals(succ, client.invokeRPC(rpcStub::getCounter));

        executor.shutdown();

        // TODO make sure continue as new is happening when no state is executed
        // https://github.com/superdurable/dex/issues/339

        client.stopWorkflow(wfId, null);
    }

    @Test
    public void testRpcNoPersistence() {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testRpcWithNoPersistence" + System.currentTimeMillis() / 1000;
        final String runId = client.startWorkflow(
                RpcWorkflow.class, wfId, 10, 999);

        final RpcWorkflow rpcStub = client.newRpcStub(RpcWorkflow.class, wfId, "" );
        client.invokeRPC(rpcStub::testRpcNoPersistence);
        WorkflowRpcRequest request = client.getUnregisteredClient().getLastOutgoingWorkflowRpcRequest();
        Assertions.assertNotNull(request.getDataAttributesLoadingPolicy());
        Assertions.assertEquals(PersistenceLoadingType.NONE,
                request.getDataAttributesLoadingPolicy().getPersistenceLoadingType());
        Assertions.assertNotNull(request.getSearchAttributesLoadingPolicy());
        Assertions.assertEquals(PersistenceLoadingType.NONE,
                request.getSearchAttributesLoadingPolicy().getPersistenceLoadingType());

        final Integer output = client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        RpcWorkflowState2.resetCounter();
        Assertions.assertEquals(2, output);
    }

    @Test
    public void testRPCWorkflowFunc1() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testRPCWorkflowFunc1" + System.currentTimeMillis() / 1000;
        final String runId = client.startWorkflow(
                RpcWorkflow.class, wfId, 10, 999);

        final RpcWorkflow rpcStub = client.newRpcStub(RpcWorkflow.class, wfId, "" );

        client.invokeRPC(rpcStub::testRpcSetDataAttribute, "test-value");
        String value = client.invokeRPC(rpcStub::testRpcGetDataAttribute);
        Assertions.assertEquals("test-value", value);
        client.invokeRPC(rpcStub::testRpcSetDataAttribute, null);
        value = client.invokeRPC(rpcStub::testRpcGetDataAttribute);
        Assertions.assertNull(value);

        client.invokeRPC(rpcStub::testRpcSetKeyword, "test-value");
        value = client.invokeRPC(rpcStub::testRpcGetKeyword);
        Assertions.assertEquals("test-value", value);
        client.invokeRPC(rpcStub::testRpcSetKeyword, null);
        value = client.invokeRPC(rpcStub::testRpcGetKeyword);
        Assertions.assertNull(value);

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
    public void testRPCWorkflowFunc0() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testRPCWorkflowFunc0" + System.currentTimeMillis() / 1000;
        final String runId = client.startWorkflow(
                RpcWorkflow.class, wfId, 10, 999);

        final RpcWorkflow rpcStub = client.newRpcStub(RpcWorkflow.class, wfId);
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
    public void testRPCWorkflowProc1() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testRPCWorkflowProc1" + System.currentTimeMillis() / 1000;
        final String runId = client.startWorkflow(
                RpcWorkflow.class, wfId, 10, 999);

        final RpcWorkflow rpcStub = client.newRpcStub(RpcWorkflow.class, wfId, "");
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
    public void testRPCWorkflowProc0() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testRPCWorkflowProc0" + System.currentTimeMillis() / 1000;
        final String runId = client.startWorkflow(
                RpcWorkflow.class, wfId, 10, 999);

        final RpcWorkflow rpcStub = client.newRpcStub(RpcWorkflow.class, wfId, "");
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
    public void testRPCWorkflowFunc1ReadOnly() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testRPCWorkflowFunc1ReadOnly" + System.currentTimeMillis() / 1000;
        final String runId = client.startWorkflow(
                RpcWorkflow.class, wfId, 10, 999);

        final RpcWorkflow rpcStub = client.newRpcStub(RpcWorkflow.class, wfId, "");
        final Long rpcOutput = client.invokeRPC(rpcStub::testRpcFunc1Readonly, RPC_INPUT);

        Assertions.assertEquals(RPC_OUTPUT, rpcOutput);

        client.stopWorkflow(wfId, "", ImmutableStopWorkflowOptions.builder()
                .workflowStopType(WorkflowStopType.FAIL)
                .reason(HARDCODED_STR)
                .build());

    }

    @Test
    public void testRpcError() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testRpcError" + System.currentTimeMillis() / 1000;
        client.startWorkflow(
                NoStateWorkflow.class, wfId, 10, 999);

        final NoStateWorkflow rpcStub = client.newRpcStub(NoStateWorkflow.class, wfId, "");

        try {
            client.invokeRPC(rpcStub::testRpcFunc1Error, RPC_INPUT);
        } catch (ClientSideException e) {
            Assertions.assertEquals(420, e.getStatusCode());
            final ErrorResponse errResp = e.getErrorResponse();
            Assertions.assertEquals(501, errResp.getOriginalWorkerErrorStatus());
            Assertions.assertTrue(errResp.getOriginalWorkerErrorDetail().contains("this is an error"));
            Assertions.assertTrue(errResp.getOriginalWorkerErrorType().contains("java.lang.RuntimeException"));
            Assertions.assertTrue(errResp.getDetail().contains("worker API error, status:501, errorType:java.lang.RuntimeException"));
        }
        client.stopWorkflow(wfId, null);
    }

    @Test
    public void testSignalChannelSizeInfo(){
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testSignalChannelSizeInfo" + System.currentTimeMillis() / 1000;
        client.startWorkflow(
                DeadEndStateWorkflow.class, wfId, 10);
        final DeadEndStateWorkflow rpcStub = client.newRpcStub(DeadEndStateWorkflow.class, wfId, "");
        client.invokeRPC(rpcStub::sendAndGetInternalChannelSize);
        final Integer size1 = client.invokeRPC(rpcStub::sendAndGetInternalChannelSize);
        Assertions.assertEquals(2, size1);

        client.signalWorkflow(DeadEndStateWorkflow.class, wfId, DeadEndStateWorkflow.IDLE_SIGNAL_CHANNEL, null);
        client.signalWorkflow(DeadEndStateWorkflow.class, wfId, DeadEndStateWorkflow.IDLE_SIGNAL_CHANNEL, null);
        client.signalWorkflow(DeadEndStateWorkflow.class, wfId, DeadEndStateWorkflow.IDLE_SIGNAL_CHANNEL, null);
        final Integer size2 = client.invokeRPC(rpcStub::getSignalChannelSize);
        Assertions.assertEquals(3, size2);

    }
}
