/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex;

/**
 * Defines one Attribute lock for a Step's {@code waitFor} or {@code execute} method.
 *
 * <p>Add locks through {@link StepOptions.Builder#addWaitForLock} or
 * {@link StepOptions.Builder#addExecuteLock}. Scalar Attributes lock their single value; an
 * Attribute map lock targets only the supplied instance. RPC methods support the same locking
 * behavior through {@link RPC#lockAttributes} and {@link RPC#lockAttributeMaps}; Java annotations
 * cannot accept {@code AttributeLock} objects, so RPC locks use annotation elements instead.
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
     * Locks a scalar Attribute.
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
     * @param instance the nonblank map instance to lock
     * @return a lock definition for that map instance
     * @throws IllegalArgumentException if {@code instance} is {@code null} or blank
     */
    public static AttributeLock of(final AttributeMap<?> attribute, final String instance) {
        return new AttributeLock(attribute.getName(), Attribute.requireName(instance));
    }

    String getAttribute() {
        return attribute;
    }

    String getInstance() {
        return instance;
    }
}
