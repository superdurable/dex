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
 * Defines a typed best-effort resumable message Stream owned by one Flow type.
 *
 * <p>Register the exact definition in one {@link PersistenceSchema}. All instances of that Flow
 * type share the approximate byte budget. Step writes are immediate and do not roll back when the
 * handler later fails.
 *
 * @param <T> the message type
 */
public final class Stream<T> extends PersistenceDefinition {
    private final String name;
    private final Class<T> valueType;
    private final long maxEstimatedBytes;

    private Stream(
            final String name,
            final Class<T> valueType,
            final long maxEstimatedBytes) {
        this.name = Attribute.requireName(name);
        if (valueType == null) {
            throw new IllegalArgumentException("Stream value type is required");
        }
        if (maxEstimatedBytes <= 0) {
            throw new IllegalArgumentException("Stream maxEstimatedBytes must be positive");
        }
        this.valueType = valueType;
        this.maxEstimatedBytes = maxEstimatedBytes;
    }

    /**
     * Creates a typed Stream definition.
     *
     * @param name the nonblank logical name unique within its Flow
     * @param valueType the concrete message class used for decoding
     * @param maxEstimatedBytes the positive approximate shared byte budget
     * @param <T> the message type
     * @return an immutable Stream definition
     */
    public static <T> Stream<T> define(
            final String name,
            final Class<T> valueType,
            final long maxEstimatedBytes) {
        return new Stream<T>(name, valueType, maxEstimatedBytes);
    }

    /**
     * Appends one message immediately from the current Step execution.
     *
     * @param context the current Step Context; RPC Contexts are rejected
     * @param value the typed message to append
     */
    public void write(final Context context, final T value) {
        context.writeStream(this, value);
    }

    /**
     * Returns the logical Stream name.
     *
     * @return the stable protocol name
     */
    public String getStreamName() {
        return name;
    }

    /**
     * Returns the approximate shared byte budget.
     *
     * @return the positive budget in bytes
     */
    public long getMaxEstimatedBytes() {
        return maxEstimatedBytes;
    }

    @Override
    String getName() {
        return name;
    }

    @Override
    Class<?> getValueType() {
        return valueType;
    }
}
