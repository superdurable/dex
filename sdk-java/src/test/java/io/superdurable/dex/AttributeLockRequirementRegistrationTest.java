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

import io.superdurable.dex.exceptions.FlowDefinitionException;
import io.superdurable.dex.exceptions.MissingAttributeLockAccess;
import io.superdurable.dex.exceptions.MissingAttributeLockException;
import org.junit.jupiter.api.Test;

import java.util.Arrays;
import java.util.Collections;

import static org.junit.jupiter.api.Assertions.assertEquals;
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
        assertSame(LockRequirement.Mode.OWN_KEY,
                Attribute.define("a", String.class).writesRequireLock()
                        .syncToAttributeStore().getLockRequirement().getMode());
        assertSame(LockRequirement.Mode.OWN_KEY,
                Attribute.define("a", String.class).writesRequireLock()
                        .hardDeleteOnNull().getLockRequirement().getMode());
        assertEquals("lock-req-coord",
                AttributeMap.define("m", String.class).writesRequireAttributeLock(coordination)
                        .syncToAttributeStore().getLockRequirement().getLockAttributeName());
        assertEquals("lock-req-coord",
                AttributeMap.define("m", String.class).writesRequireAttributeLock(coordination)
                        .hardDeleteOnNull().getLockRequirement().getLockAttributeName());
        assertTrue(Attribute.define("a", String.class).syncToAttributeStore()
                .writesRequireLock().isSyncToAttributeStore());
        assertTrue(AttributeMap.define("m", String.class).hardDeleteOnNull()
                .writesRequireLock().isHardDeleteOnNull());
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

    private static void assertRegistrationFails(
            final String expectedFragment,
            final PersistenceSchema schema) {
        final FlowDefinitionException failure = assertThrows(
                FlowDefinitionException.class,
                () -> new Registry(Collections.<Flow<?>>singletonList(
                        new LockRequirementSchemaFlow(schema))));
        assertTrue(failure.getMessage().contains(expectedFragment), failure.getMessage());
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
