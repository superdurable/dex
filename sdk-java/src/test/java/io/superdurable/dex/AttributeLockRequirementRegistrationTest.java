/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.superdurable.dex.exceptions.FlowDefinitionException;
import io.superdurable.dex.exceptions.MissingAttributeLockAccess;
import io.superdurable.dex.exceptions.MissingAttributeLockException;
import io.superdurable.gen.ChannelInfo;
import io.superdurable.gen.KV;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;
import java.util.Objects;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class AttributeLockRequirementRegistrationTest {
    private static final Attribute<Boolean> MISSING_LOCK =
            Attribute.define("lock-req-missing", Boolean.class);
    // An AttributeMap registered under the lock Attribute's name.
    private static final Attribute<Boolean> MAP_LOCK =
            Attribute.define("lock-req-map-lock", Boolean.class);
    private static final AttributeMap<Boolean> MAP_LOCK_COLLISION =
            AttributeMap.define("lock-req-map-lock", Boolean.class);

    @Test
    void rejectsAttributeWriteLockMissingFromSchema() {
        assertRegistrationFails(
                "Attribute lock-req-a write lock: lock Attribute lock-req-missing is not in the "
                        + "PersistenceSchema",
                PersistenceSchema.of(Attribute.define("lock-req-a", Integer.class)
                        .writesRequireAttributeLock(MISSING_LOCK)));
    }

    @Test
    void rejectsAttributeMapWriteLockMissingFromSchema() {
        assertRegistrationFails(
                "AttributeMap lock-req-a write lock: lock Attribute lock-req-missing is not in the "
                        + "PersistenceSchema",
                PersistenceSchema.of(AttributeMap.define("lock-req-a", Integer.class)
                        .writesRequireAttributeLock(MISSING_LOCK)));
    }

    @Test
    void rejectsChannelMapPublishLockMissingFromSchema() {
        assertRegistrationFails(
                "ChannelMap lock-req-c publish lock: lock Attribute lock-req-missing is not in the "
                        + "PersistenceSchema",
                PersistenceSchema.of(ChannelMap.define("lock-req-c", String.class)
                        .publishesRequireAttributeLock(MISSING_LOCK)));
    }

    @Test
    void rejectsChannelPublishLockNamedLikeAnotherChannel() {
        // A Channel registered under the lock Attribute's name is not a lock Attribute.
        assertRegistrationFails(
                "Channel lock-req-c publish lock: lock Attribute lock-req-missing is not in the "
                        + "PersistenceSchema",
                PersistenceSchema.of(
                        Channel.define("lock-req-missing", String.class),
                        Channel.define("lock-req-c", String.class)
                                .publishesRequireAttributeLock(MISSING_LOCK)));
    }

    @Test
    void rejectsAttributeMapRegisteredUnderLockName() {
        assertRegistrationFails(
                "Channel lock-req-c publish lock: lock Attribute lock-req-map-lock is an "
                        + "AttributeMap; use a singleton Attribute",
                PersistenceSchema.of(
                        MAP_LOCK_COLLISION,
                        Channel.define("lock-req-c", String.class)
                                .publishesRequireAttributeLock(MAP_LOCK)));
    }

    @Test
    void rejectsNullLockAttributeAtDeclaration() {
        // Java cannot define an Attribute with an empty name, so null is the only empty lock.
        assertThrows(NullPointerException.class,
                () -> Attribute.define("lock-req-a", Integer.class)
                        .writesRequireAttributeLock(null));
        assertThrows(NullPointerException.class,
                () -> AttributeMap.define("lock-req-a", Integer.class)
                        .writesRequireAttributeLock(null));
        assertThrows(NullPointerException.class,
                () -> Channel.define("lock-req-c", String.class)
                        .publishesRequireAttributeLock(null));
        assertThrows(NullPointerException.class,
                () -> ChannelMap.define("lock-req-c", String.class)
                        .publishesRequireAttributeLock(null));
        assertThrows(IllegalArgumentException.class,
                () -> Attribute.define("", Boolean.class));
    }

    @Test
    void acceptsRegisteredSingletonLockAttribute() {
        final Attribute<Boolean> coordination = Attribute.define("lock-req-coord", Boolean.class);
        new Registry(Collections.<Flow<?>>singletonList(new LockRequirementSchemaFlow(
                PersistenceSchema.of(
                        coordination,
                        AttributeMap.define("lock-req-a", Integer.class)
                                .writesRequireAttributeLock(coordination),
                        Channel.define("lock-req-c", String.class)
                                .publishesRequireAttributeLock(coordination)))));
    }

    @Test
    void otherDefinitionOptionsKeepTheWriteLock() {
        final Attribute<Boolean> coordination = Attribute.define("lock-req-coord", Boolean.class);
        final Attribute<String> attribute = Attribute.define("a", String.class);
        final AttributeMap<String> map = AttributeMap.define("m", String.class);
        // Each definition, modifier, and declaration, in both chaining orders.
        assertOwnKey(attribute.writesRequireLock().syncToAttributeStore());
        assertOwnKey(attribute.writesRequireLock().hardDeleteOnNull());
        assertOwnKey(attribute.syncToAttributeStore().writesRequireLock());
        assertOwnKey(attribute.hardDeleteOnNull().writesRequireLock());
        assertCoordination(attribute.writesRequireAttributeLock(coordination).syncToAttributeStore());
        assertCoordination(attribute.writesRequireAttributeLock(coordination).hardDeleteOnNull());
        assertCoordination(attribute.syncToAttributeStore().writesRequireAttributeLock(coordination));
        assertCoordination(attribute.hardDeleteOnNull().writesRequireAttributeLock(coordination));
        assertOwnKey(map.writesRequireLock().syncToAttributeStore());
        assertOwnKey(map.writesRequireLock().hardDeleteOnNull());
        assertOwnKey(map.syncToAttributeStore().writesRequireLock());
        assertOwnKey(map.hardDeleteOnNull().writesRequireLock());
        assertCoordination(map.writesRequireAttributeLock(coordination).syncToAttributeStore());
        assertCoordination(map.writesRequireAttributeLock(coordination).hardDeleteOnNull());
        assertCoordination(map.syncToAttributeStore().writesRequireAttributeLock(coordination));
        assertCoordination(map.hardDeleteOnNull().writesRequireAttributeLock(coordination));
        // The declaration keeps the modifiers applied before it.
        assertTrue(attribute.syncToAttributeStore().writesRequireLock().isSyncToAttributeStore());
        assertTrue(attribute.hardDeleteOnNull().writesRequireAttributeLock(coordination)
                .isHardDeleteOnNull());
        assertTrue(map.syncToAttributeStore().writesRequireAttributeLock(coordination)
                .isSyncToAttributeStore());
        assertTrue(map.hardDeleteOnNull().writesRequireLock().isHardDeleteOnNull());
        // The last declaration wins.
        assertSame(LockRequirement.Mode.OWN_KEY,
                Attribute.define("a", String.class).writesRequireAttributeLock(coordination)
                        .writesRequireLock().getLockRequirement().getMode());
        assertNull(Attribute.define("a", String.class).getLockRequirement());
        assertNull(Channel.define("c", String.class).getLockRequirement());
    }

    @Test
    void exceptionMessageNamesHandlerAccessesAndLocks() {
        final MissingAttributeLockException step = new MissingAttributeLockException(
                "OrderFlow",
                "AddItemStep",
                MissingAttributeLockException.HandlerMethod.EXECUTE,
                null,
                Arrays.asList(
                        new MissingAttributeLockAccess(
                                MissingAttributeLockAccess.Kind.ATTRIBUTE_MAP,
                                "items", "item a", "items", "item a"),
                        new MissingAttributeLockAccess(
                                MissingAttributeLockAccess.Kind.CHANNEL,
                                "replies", null, "conversation-lock", null)));
        assertEquals(
                "Flow \"OrderFlow\" Step \"AddItemStep\" execute wrote AttributeMap \"items\" "
                        + "instance \"item a\" without the required lock of AttributeMap \"items\" "
                        + "instance \"item a\", published to Channel \"replies\" without the "
                        + "required lock of Attribute \"conversation-lock\"; register the lock in "
                        + "the handler's StepOptions or @RPC locks",
                step.getMessage());
        assertEquals(2, step.getAccesses().size());
        assertThrows(UnsupportedOperationException.class, () -> step.getAccesses().clear());
        final MissingAttributeLockException rpc = new MissingAttributeLockException(
                "OrderFlow",
                null,
                MissingAttributeLockException.HandlerMethod.RPC,
                "addItem",
                Collections.singletonList(new MissingAttributeLockAccess(
                        MissingAttributeLockAccess.Kind.ATTRIBUTE,
                        "total", null, "total", null)));
        assertEquals(
                "Flow \"OrderFlow\" RPC \"addItem\" wrote Attribute \"total\" without the "
                        + "required lock of Attribute \"total\"; register the lock in the "
                        + "handler's StepOptions or @RPC locks",
                rpc.getMessage());
    }

    @Test
    void exceptionRejectsEmptyAccesses() {
        assertThrows(IllegalArgumentException.class, () -> new MissingAttributeLockException(
                "OrderFlow", "AddItemStep", MissingAttributeLockException.HandlerMethod.EXECUTE,
                null, Collections.<MissingAttributeLockAccess>emptyList()));
    }

    @Test
    void flowWithoutDeclarationsKeepsReadingStepOptionsAtRequestTime() {
        // Before declared lock requirements, Registry never called getStepOptions().
        final SchemaInitializedOptionsFlow flow = new SchemaInitializedOptionsFlow(false);
        final Registry registry = new Registry(Collections.<Flow<?>>singletonList(flow));
        assertEquals(0, flow.optionsReads.get());
        final Registry.RegisteredStep step =
                registry.getFlow(flow.getFlowType()).getStep("SchemaInitializedOptionsStep");
        step.getDefaultStepOptions();
        step.getDefaultStepOptions();
        assertEquals(2, flow.optionsReads.get());
        assertFalse(registry.getFlow(flow.getFlowType()).hasLockRequirements());
    }

    @Test
    void flowWithDeclarationsReadsStepOptionsOnceAfterSchema() {
        final SchemaInitializedOptionsFlow flow = new SchemaInitializedOptionsFlow(true);
        final Registry registry = new Registry(Collections.<Flow<?>>singletonList(flow));
        assertEquals(1, flow.optionsReads.get());
        final Registry.RegisteredFlow registered = registry.getFlow(flow.getFlowType());
        final Registry.RegisteredStep step = registered.getStep("SchemaInitializedOptionsStep");
        final StepOptions first = step.getDefaultStepOptions();
        assertSame(first, step.getDefaultStepOptions());
        assertSame(step, registered.getStartStep());
        assertSame(step, registered.getStep(SchemaInitializedOptionsFlow.SchemaInitializedOptionsStep.class));
        assertEquals(1, flow.optionsReads.get());
        assertEquals(Collections.singleton("lock-req-total"), step.getExecuteLockKeys());
        assertTrue(registered.hasLockRequirements());
    }

    @Test
    void concurrentHandlerThreadsBufferEveryChange() throws Exception {
        final Attribute<Integer> total = Attribute.define("lock-req-total", Integer.class);
        final AttributeMap<Integer> items = AttributeMap.define("lock-req-items", Integer.class);
        final Channel<String> replies = Channel.define("lock-req-replies", String.class);
        final Registry registry = new Registry(Collections.<Flow<?>>singletonList(
                new LockRequirementSchemaFlow(PersistenceSchema.of(total, items, replies))));
        final InvocationContext context = new InvocationContext(
                InvocationContext.Method.EXECUTE,
                registry.getFlow("LockRequirementSchemaFlow"),
                io.superdurable.gen.Context.getDefaultInstance(),
                new ValueMapper(new ObjectMapper()),
                null,
                Collections.<KV>emptyList(),
                Collections.<KV>emptyList(),
                null,
                Collections.<String, ChannelInfo>emptyMap());
        final int threads = 4;
        final int operations = 250;
        final CountDownLatch start = new CountDownLatch(1);
        final AtomicBoolean writing = new AtomicBoolean(true);
        final AtomicReference<Throwable> failure = new AtomicReference<Throwable>();
        final List<Thread> writers = new ArrayList<Thread>();
        for (int thread = 0; thread < threads; thread++) {
            final int writer = thread;
            writers.add(new Thread(() -> {
                try {
                    start.await();
                    for (int index = 0; index < operations; index++) {
                        items.set(context, "w" + writer + "-" + index, index);
                        replies.publish(context, "reply");
                        replies.delete(context, "message-" + writer + "-" + index);
                    }
                } catch (Throwable error) {
                    failure.compareAndSet(null, error);
                }
            }));
        }
        final Thread reader = new Thread(() -> {
            try {
                start.await();
                while (writing.get()) {
                    final InvocationContext.BufferedChanges changes =
                            context.snapshotBufferedChanges();
                    assertTrue(changes.attributeWrites.size() <= threads * operations);
                    context.channelSize(replies);
                }
            } catch (Throwable error) {
                failure.compareAndSet(null, error);
            }
        });
        reader.start();
        for (Thread writer : writers) {
            writer.start();
        }
        start.countDown();
        for (Thread writer : writers) {
            writer.join(30_000L);
        }
        writing.set(false);
        reader.join(30_000L);
        if (failure.get() != null) {
            throw new AssertionError(failure.get());
        }
        final InvocationContext.BufferedChanges changes = context.snapshotBufferedChanges();
        assertEquals(threads * operations, changes.attributeWrites.size());
        assertEquals(threads * operations, changes.publications.size());
        assertEquals(threads * operations, changes.channelDeletions.size());
        assertThrows(UnsupportedOperationException.class, () -> changes.publications.clear());
    }

    private static void assertOwnKey(final PersistenceDefinition definition) {
        assertSame(LockRequirement.Mode.OWN_KEY, definition.getLockRequirement().getMode());
    }

    private static void assertCoordination(final PersistenceDefinition definition) {
        assertSame(LockRequirement.Mode.ATTRIBUTE, definition.getLockRequirement().getMode());
        assertEquals("lock-req-coord", definition.getLockRequirement().getLockAttributeName());
    }

    private static void assertRegistrationFails(
            final String expectedFragment,
            final PersistenceSchema schema) {
        final FlowDefinitionException failure = assertThrows(
                FlowDefinitionException.class,
                () -> new Registry(Collections.<Flow<?>>singletonList(
                        new LockRequirementSchemaFlow(schema))));
        assertTrue(failure.getMessage().contains(expectedFragment), failure.getMessage());
    }

    /** A Flow whose schema callback initializes the field that its Step options read. */
    static final class SchemaInitializedOptionsFlow implements Flow<Void> {
        private final boolean declaresLockRequirement;
        private final AtomicInteger optionsReads = new AtomicInteger();
        private Attribute<Integer> total;

        SchemaInitializedOptionsFlow(final boolean declaresLockRequirement) {
            this.declaresLockRequirement = declaresLockRequirement;
        }

        @Override
        public StepList<Void> getSteps() {
            return StepList.startStep(new SchemaInitializedOptionsStep());
        }

        @Override
        public PersistenceSchema getPersistenceSchema() {
            final Attribute<Integer> defined = Attribute.define("lock-req-total", Integer.class);
            total = declaresLockRequirement ? defined.writesRequireLock() : defined;
            return PersistenceSchema.of(total);
        }

        final class SchemaInitializedOptionsStep implements Step<Void> {
            @Override
            public Class<Void> getInputType() {
                return Void.class;
            }

            @Override
            public StepOptions getStepOptions() {
                optionsReads.incrementAndGet();
                // Throws NullPointerException when called before getPersistenceSchema().
                return StepOptions.newBuilder()
                        .addExecuteLock(AttributeLock.of(Objects.requireNonNull(total, "total")))
                        .build();
            }

            @Override
            public StepDecision execute(final Context context, final Void input) {
                return StepDecision.gracefulComplete();
            }
        }
    }

    static final class LockRequirementSchemaFlow implements Flow<Void> {
        private final PersistenceSchema schema;

        LockRequirementSchemaFlow(final PersistenceSchema schema) {
            this.schema = schema;
        }

        @Override
        public PersistenceSchema getPersistenceSchema() {
            return schema;
        }
    }
}
