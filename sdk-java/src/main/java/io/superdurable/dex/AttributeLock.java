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

/**
 * Defines one Attribute lock for a Step's {@code waitFor} or {@code execute} method.
 *
 * <p>Add locks through {@link StepOptions.Builder#addWaitForLock} or
 * {@link StepOptions.Builder#addExecuteLock}. An Attribute lock targets its single stored value; an
 * Attribute-map lock targets only the supplied instance. RPC methods support the same locking
 * behavior through {@link RPC#lockAttributes} and {@link RPC#lockAttributeMaps}; Java annotations
 * cannot accept {@code AttributeLock} objects, so RPC locks use annotation elements instead.
 *
 * <p>Locks serialize participating writers, not all reads. A Step waits until its locks are
 * available; its method timeout starts only at Worker hand-out. An RPC that declares locks fails
 * with {@link io.superdurable.dex.exceptions.RpcLockConflictException} instead of waiting.
 * Declare locks consistently on every Step and RPC that updates the protected value.
 *
 * <p>With Server protocol 4 (server-zd v0.1.17, required by SDK 1.35.0): the first attempt
 * takes its locks at Worker hand-out. Retries and returned attempts retain their locks while
 * queued, until the method finishes or the Flow ends.
 * A Step type unsupported by all connected Workers can hold those locks until a capable Worker
 * connects. Locking a map instance does not load its value; request state loads separately.
 * To protect a collection-wide invariant, use a separate singleton Attribute as a coordination
 * lock and request it from every participating writer. Its stored value is not lock state.
 *
 * <pre>{@code
 * return StepOptions.newBuilder()
 *         .addExecuteLock(AttributeLock.of(balance))
 *         .addExecuteLock(AttributeLock.of(orderStatus, orderId))
 *         .build();
 * }</pre>
 */
public final class AttributeLock {
    private final String attribute;
    private final String instance;

    private AttributeLock(final String attribute, final String instance) {
        this.attribute = attribute;
        this.instance = instance;
    }

    /**
     * Locks the value stored by an Attribute.
     *
     * @param attribute the registered Attribute to lock
     * @return a lock definition for that Attribute
     */
    public static AttributeLock of(final Attribute<?> attribute) {
        return new AttributeLock(attribute.getName(), null);
    }

    /**
     * Locks one instance of an Attribute map.
     *
     * @param attribute the registered Attribute map
     * @param instance the map instance to lock. Slash is prohibited because it is a reserved character
     * @return a lock definition for that map instance
     * @throws IllegalArgumentException if {@code instance} is {@code null}, blank, or contains {@code /}
     */
    public static AttributeLock of(final AttributeMap<?> attribute, final String instance) {
        return new AttributeLock(attribute.getName(), Attribute.requireMapInstance(instance));
    }

    String getAttribute() {
        return attribute;
    }

    String getInstance() {
        return instance;
    }
}
