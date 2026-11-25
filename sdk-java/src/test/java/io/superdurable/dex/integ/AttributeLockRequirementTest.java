/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Sustainable Use License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.AttributeRead;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.RPCInvokeOptions;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.exceptions.MissingAttributeLockException;
import io.superdurable.dex.exceptions.RpcLockConflictException;
import io.superdurable.dex.exceptions.WorkerInvocationException;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.Optional;
import java.util.UUID;
import java.util.concurrent.TimeUnit;

import static io.superdurable.dex.integ.AttributeLockRequirementStepWorkflow.EVENTS;
import static io.superdurable.dex.integ.AttributeLockRequirementStepWorkflow.FREE;
import static io.superdurable.dex.integ.AttributeLockRequirementStepWorkflow.ITEMS;
import static io.superdurable.dex.integ.AttributeLockRequirementStepWorkflow.OTHER_INSTANCE;
import static io.superdurable.dex.integ.AttributeLockRequirementStepWorkflow.SHARED;
import static io.superdurable.dex.integ.AttributeLockRequirementStepWorkflow.SPACED_INSTANCE;
import static io.superdurable.dex.integ.AttributeLockRequirementStepWorkflow.TOTAL;
import static io.superdurable.dex.integ.AttributeLockRequirementStepWorkflow.WRITTEN;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

@Tag("dex-dev")
public final class AttributeLockRequirementTest {
    private static final String MISSING_LOCK_TYPE = MissingAttributeLockException.class.getName();
    private static final Duration WAIT = Duration.ofSeconds(60);
    private static final AttributeLockRequirementStepWorkflow STEP_FLOW =
            new AttributeLockRequirementStepWorkflow();
    private static final AttributeLockRequirementRpcWorkflow RPC_FLOW =
            new AttributeLockRequirementRpcWorkflow();
    private static final AttributeLockRequirementTimeoutWorkflow TIMEOUT_FLOW =
            new AttributeLockRequirementTimeoutWorkflow();

    @TempDir
    Path cacheDirectory;

    /** A failed Step leaves every key it wrote unset; a passing Step commits them. */
    @Test
    void testDeclaredWritesFromStepMethods() throws Exception {
        try (DexDevTestEnvironment environment = start()) {
            assertStepCase(environment, "unlocked-total",
                    "Step \"LockReqUnlockedTotalStep\" execute wrote Attribute \"lock-req-total\" "
                            + "without the required lock of Attribute \"lock-req-total\"",
                    TOTAL);
            assertStepCase(environment, "movement-lock-total",
                    "wrote Attribute \"lock-req-total\"", TOTAL);
            assertStepCase(environment, "wait-for-total",
                    "Step \"LockReqWaitForTotalStep\" waitFor wrote Attribute \"lock-req-total\"",
                    TOTAL);
            // The second key is written from another thread of the same handler.
            assertStepCase(environment, "other-item",
                    "wrote AttributeMap \"lock-req-items\" instance \"item b\" without the "
                            + "required lock of AttributeMap \"lock-req-items\" instance \"item b\"",
                    ITEMS.instance(SPACED_INSTANCE), ITEMS.instance(OTHER_INSTANCE));
            assertStepCase(environment, "unlocked-shared",
                    "wrote AttributeMap \"lock-req-shared\" instance \"x\" without the required "
                            + "lock of Attribute \"lock-req-coordination\"",
                    SHARED.instance("x"));
            assertStepCase(environment, "locked-wait-for-total", null, TOTAL);
            assertStepCase(environment, "locked-total", null, TOTAL);
            assertStepCase(environment, "locked-item", null, ITEMS.instance(SPACED_INSTANCE));
            assertStepCase(environment, "locked-shared", null, SHARED.instance("x"));
            assertStepCase(environment, "undeclared", null, FREE);
        }
    }

    /** The started Step holds the checked lock even when later getStepOptions() calls drop it. */
    @Test
    void testStartedStepHoldsTheCapturedLock() throws Exception {
        final AttributeLockRequirementChangingOptionsWorkflow flow =
                new AttributeLockRequirementChangingOptionsWorkflow();
        try (DexDevTestEnvironment environment =
                DexDevTestEnvironment.start(cacheDirectory, flow)) {
            assertEquals(1, flow.optionsReads.get());
            final String flowId = "lock-req-changing-options-" + UUID.randomUUID();
            environment.client().startFlow(flow, flowId, "start");
            assertTrue(flow.executeEntered.await(60, TimeUnit.SECONDS));
            final AttributeLockRequirementChangingOptionsWorkflow stub = environment.client()
                    .newRpcStub(AttributeLockRequirementChangingOptionsWorkflow.class, flowId);
            assertThrows(RpcLockConflictException.class,
                    () -> environment.client().invokeRPC(stub::setTotalLocked, 7),
                    "the running Step must hold the lock-req-total lock");
            flow.releaseExecute.countDown();
            final FlowResult result = environment.client().waitForFlow(flowId, WAIT);
            assertEquals(FlowStatus.COMPLETED, result.getStatus(), result.getErrorMessage());
            assertEquals(WRITTEN, result.getSingleOutput(String.class));
            assertEquals(Optional.of(1), read(environment, flowId, TOTAL));
            assertEquals(1, flow.optionsReads.get());
        } finally {
            flow.releaseExecute.countDown();
        }
    }

    @Test
    void testTimeoutHandlerIsNotChecked() throws Exception {
        try (DexDevTestEnvironment environment = start()) {
            final String flowId = "lock-req-timeout-" + UUID.randomUUID();
            environment.client().startFlow(TIMEOUT_FLOW, flowId, null,
                    StartFlowOptions.newBuilder().timeout(Duration.ofSeconds(1)).build());
            final FlowResult result = environment.client().waitForFlow(flowId, WAIT);
            assertEquals(FlowStatus.COMPLETED, result.getStatus(), result.getErrorMessage());
            assertEquals(AttributeLockRequirementTimeoutWorkflow.EXPIRED,
                    result.getSingleOutput(String.class));
            assertEquals(Optional.of(5), read(environment, flowId, TOTAL));
        }
    }

    @Test
    void testDeclaredWritesAndPublishesFromRpcs() throws Exception {
        try (DexDevTestEnvironment environment = start()) {
            // A registered lock commits the publish; an unlocked one is discarded with its writes.
            String flowId = startRpcFlow(environment);
            AttributeLockRequirementRpcWorkflow stub = stub(environment, flowId);
            environment.client().invokeRPC(stub::publishReplyLocked, "hi");
            assertEquals(1, (int) environment.client().invokeRPC(stub::countReplies));
            final AttributeLockRequirementRpcWorkflow replyStub = stub;
            assertMissingLock(
                    () -> environment.client().invokeRPC(replyStub::publishReplyUnlocked, "hi"),
                    "RPC \"publishReplyUnlocked\" published to Channel \"lock-req-replies\" "
                            + "without the required lock of Attribute \"lock-req-coordination\"");
            assertEquals(1, (int) environment.client().invokeRPC(stub::countReplies));
            assertFalse(read(environment, flowId, FREE).isPresent());

            // An unlocked AttributeMap write fails.
            flowId = startRpcFlow(environment);
            final AttributeLockRequirementRpcWorkflow itemStub = stub(environment, flowId);
            assertMissingLock(
                    () -> environment.client().invokeRPC(itemStub::setItem, SPACED_INSTANCE),
                    "wrote AttributeMap \"lock-req-items\" instance \"item a\"");
            assertFalse(read(environment, flowId, ITEMS.instance(SPACED_INSTANCE)).isPresent());

            // A call-time lock does not satisfy the declared requirement.
            assertMissingLock(
                    () -> environment.client().invokeRPC(
                            itemStub::setItem,
                            SPACED_INSTANCE,
                            RPCInvokeOptions.newBuilder()
                                    .addLockAttributeMapInstance(ITEMS, SPACED_INSTANCE)
                                    .build()),
                    "wrote AttributeMap \"lock-req-items\" instance \"item a\"");
            assertFalse(read(environment, flowId, ITEMS.instance(SPACED_INSTANCE)).isPresent());

            // A registered annotation lock commits the write.
            environment.client().invokeRPC(itemStub::setItemLocked, SPACED_INSTANCE);
            assertEquals(Optional.of(1), read(environment, flowId, ITEMS.instance(SPACED_INSTANCE)));

            // An unlocked ChannelMap publish fails and leaves the queue empty.
            flowId = startRpcFlow(environment);
            final AttributeLockRequirementRpcWorkflow eventStub = stub(environment, flowId);
            assertMissingLock(
                    () -> environment.client().invokeRPC(
                            eventStub::publishEventUnlocked, SPACED_INSTANCE),
                    "published to ChannelMap \"lock-req-events\" instance \"item a\" without the "
                            + "required lock of Attribute \"lock-req-coordination\"");
            assertEquals(0, (int) environment.client().invokeRPC(
                    eventStub::countEvents, SPACED_INSTANCE));
            assertFalse(read(environment, flowId, FREE).isPresent());

            // An unlocked delete fails and the stored value stays.
            flowId = startRpcFlow(environment);
            final AttributeLockRequirementRpcWorkflow totalStub = stub(environment, flowId);
            environment.client().invokeRPC(totalStub::setTotalLocked, 7);
            assertMissingLock(
                    () -> environment.client().invokeRPC(totalStub::deleteTotalUnlocked),
                    "RPC \"deleteTotalUnlocked\" wrote Attribute \"lock-req-total\" without the "
                            + "required lock of Attribute \"lock-req-total\"");
            assertEquals(Optional.of(7), read(environment, flowId, TOTAL));
        }
    }

    private DexDevTestEnvironment start() throws Exception {
        return DexDevTestEnvironment.start(cacheDirectory, STEP_FLOW, RPC_FLOW, TIMEOUT_FLOW);
    }

    /**
     * Runs one Step case and checks its output and writes.
     *
     * @param missingFragment the expected error detail fragment, or {@code null} when the Step
     *     succeeds
     */
    private static void assertStepCase(
            final DexDevTestEnvironment environment,
            final String stepCase,
            final String missingFragment,
            final AttributeRead<?>... written) {
        final String flowId = "lock-req-" + stepCase + "-" + UUID.randomUUID();
        environment.client().startFlow(STEP_FLOW, flowId, stepCase);
        final FlowResult result = environment.client().waitForFlow(flowId, WAIT);
        assertEquals(FlowStatus.COMPLETED, result.getStatus(), result.getErrorMessage());
        final String output = result.getSingleOutput(String.class);
        if (missingFragment == null) {
            assertEquals(WRITTEN, output);
        } else {
            assertMissingLock(output, missingFragment);
        }
        for (AttributeRead<?> read : written) {
            assertEquals(missingFragment == null, read(environment, flowId, read).isPresent(),
                    stepCase + " write of " + read);
        }
    }

    private static void assertMissingLock(final String output, final String fragment) {
        assertTrue(output.startsWith(MISSING_LOCK_TYPE + ": "), output);
        assertTrue(output.contains(fragment), output);
    }

    private static void assertMissingLock(final Runnable invocation, final String fragment) {
        final WorkerInvocationException failure =
                assertThrows(WorkerInvocationException.class, invocation::run);
        assertEquals(MISSING_LOCK_TYPE, failure.getWorkerErrorType());
        assertTrue(failure.getWorkerErrorDetail().contains(fragment),
                failure.getWorkerErrorDetail());
    }

    private static String startRpcFlow(final DexDevTestEnvironment environment) {
        final String flowId = "lock-req-rpc-" + UUID.randomUUID();
        environment.client().startFlow(RPC_FLOW, flowId, null);
        return flowId;
    }

    private static AttributeLockRequirementRpcWorkflow stub(
            final DexDevTestEnvironment environment,
            final String flowId) {
        return environment.client().newRpcStub(AttributeLockRequirementRpcWorkflow.class, flowId);
    }

    private static <T> Optional<T> read(
            final DexDevTestEnvironment environment,
            final String flowId,
            final AttributeRead<T> read) {
        return environment.client().getAttributes(flowId, read).get(read);
    }
}
