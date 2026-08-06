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

package io.superdurable.dex.contracts;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.BlobCache;
import io.superdurable.dex.BlobCacheConfig;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Client;
import io.superdurable.dex.ClientOptions;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCAttributeMapLock;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Registry;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDef;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.Wait;
import io.superdurable.dex.WorkerOptions;
import org.junit.jupiter.api.Assertions;
import org.junit.jupiter.api.Test;

import java.lang.reflect.Method;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;
import java.util.Optional;

public class UserContractsTest {
    private static final BlobCache BLOB_CACHE = new ContractBlobCache();
    private static final Attribute<String> STATUS = Attribute.define("status", String.class);
    private static final AttributeMap<String> ITEMS = AttributeMap.define("items", String.class);
    private static final Channel<OrderInput> COMMANDS =
            Channel.define("commands", OrderInput.class);
    private static final Step<OrderInput> APPROVE = new ApproveStep();
    private static final Flow<OrderInput> ORDERS = new OrderFlow();

    @Test
    public void typedDefinitionsBuildWithoutRuntime() {
        final Registry registry = new Registry(Collections.<Flow<?>>singletonList(ORDERS));
        Assertions.assertNotNull(registry);
        Assertions.assertEquals("OrderFlow", ORDERS.getFlowType());
        Assertions.assertEquals(OrderInput.class, APPROVE.getInputType());
        Assertions.assertEquals("GetOrder", rpcAnnotationName());
        Assertions.assertNotNull(PersistenceSchema.of(Collections.singletonList(STATUS)));
        Assertions.assertNotNull(PersistenceSchema.of(Collections.singletonList(COMMANDS)));
        Assertions.assertThrows(
                IllegalArgumentException.class,
                () -> PersistenceSchema.of(
                        Collections.singletonList(COMMANDS),
                        Collections.emptyList()));
    }

    @Test
    public void rpcAnnotationDefaultsAreAvailableThroughReflection() {
        final RPC annotation = rpcAnnotation("recordOrder");
        Assertions.assertEquals("", annotation.name());
        Assertions.assertEquals(0, annotation.timeoutSeconds());
        Assertions.assertArrayEquals(new String[0], annotation.lockAttributes());
        Assertions.assertEquals(0, annotation.lockAttributeMaps().length);
    }

    @Test
    public void rpcAnnotationSupportsNestedAnnotationArrays() {
        final RPC annotation = rpcAnnotation("getOrder");
        Assertions.assertEquals(1, annotation.lockAttributeMaps().length);
        Assertions.assertEquals("items", annotation.lockAttributeMaps()[0].attribute());
        Assertions.assertEquals("one", annotation.lockAttributeMaps()[0].instance());
    }

    @Test
    public void optionsAcceptDefaultAndCustomObjectMappers() {
        final ObjectMapper mapper = new ObjectMapper();
        final ClientOptions clientOptions = new ClientOptions("localhost:8801", null, mapper);
        final WorkerOptions workerOptions = new WorkerOptions(
                ":8803", null, "localhost:8801", 32, 64, mapper);
        Assertions.assertNotNull(clientOptions);
        Assertions.assertNotNull(workerOptions);
        Assertions.assertNotNull(new ClientOptions());
        Assertions.assertNotNull(new WorkerOptions());
    }

    @Test
    public void registryRejectsUnknownAnnotationLocks() {
        Assertions.assertThrows(
                IllegalArgumentException.class,
                () -> new Registry(Collections.<Flow<?>>singletonList(new InvalidOrderFlow())));
    }

    @Test
    public void runtimeBoundaryFailsExplicitly() {
        final Client client = new Client(
                new Registry(Collections.<Flow<?>>singletonList(ORDERS)),
                BLOB_CACHE);
        Assertions.assertThrows(
                UnsupportedOperationException.class,
                () -> client.startFlow(ORDERS, "order-1", new OrderInput()));
    }

    @Test
    public void blobCacheContractValidatesConfigBeforeNativePhase() {
        final BlobCacheConfig config = new BlobCacheConfig("contract-cache", 1024L, 0L);
        Assertions.assertThrows(
                UnsupportedOperationException.class,
                () -> BlobCache.open(config));
    }

    private static String rpcAnnotationName() {
        return rpcAnnotation("getOrder").name();
    }

    private static RPC rpcAnnotation(final String methodName) {
        try {
            final Method method = OrderFlow.class
                    .getMethod(methodName, Context.class, OrderInput.class);
            return method.getAnnotation(RPC.class);
        } catch (NoSuchMethodException exception) {
            throw new AssertionError(exception);
        }
    }

    @SuppressWarnings("unused")
    private static void compileRPCStubContract(
            final Client client,
            final OrderFlow rpcStub,
            final OrderInput input) {
        final OrderOutput output = client.invokeRPC(rpcStub::getOrder, input);
        client.invokeRPC(rpcStub::recordOrder, input);
        if (output == null) {
            throw new AssertionError("compile-only contract");
        }
    }

    public static class OrderFlow implements Flow<OrderInput> {
        @Override
        public List<StepDef> getSteps() {
            return Collections.singletonList(StepDef.startStep(APPROVE));
        }

        @Override
        public PersistenceSchema getPersistenceSchema() {
            return PersistenceSchema.of(
                    Arrays.asList(STATUS, ITEMS),
                    Collections.singletonList(COMMANDS));
        }

        @RPC(
                name = "GetOrder",
                timeoutSeconds = 10,
                lockAttributes = {"status"},
                lockAttributeMaps = {
                    @RPCAttributeMapLock(attribute = "items", instance = "one")
                })
        public RPCResult<OrderOutput> getOrder(
                final Context context,
                final OrderInput input) {
            return RPCResult.of(new OrderOutput());
        }

        @RPC
        public void recordOrder(final Context context, final OrderInput input) {
        }
    }

    public static class InvalidOrderFlow implements Flow<OrderInput> {
        @RPC(lockAttributes = {"missing"})
        public RPCResult<OrderOutput> getOrder(
                final Context context,
                final OrderInput input) {
            return RPCResult.of(new OrderOutput());
        }
    }

    public static class ApproveStep implements Step<OrderInput> {
        @Override
        public Class<OrderInput> getInputType() {
            return OrderInput.class;
        }

        @Override
        public StepDecision execute(final Context context, final OrderInput input) {
            return StepDecision.gracefulComplete(input.orderId);
        }

        @Override
        public Wait waitFor(final Context context, final OrderInput input) {
            return Wait.anyOf(COMMANDS.forOne());
        }
    }

    public static class OrderInput {
        public String orderId;
    }

    public static class OrderOutput {
        public boolean accepted;
    }

    private static final class ContractBlobCache implements BlobCache {
        @Override
        public Optional<byte[]> get(final String blobId) {
            return Optional.empty();
        }

        @Override
        public boolean put(final String blobId, final byte[] payload) {
            return false;
        }

        @Override
        public void delete(final String blobId) {
        }

        @Override
        public void deleteAll() {
        }

        @Override
        public void close() {
        }
    }
}
