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

import java.util.Collections;
import java.util.Map;
import java.util.Objects;
import java.util.Optional;

/**
 * Holds the decoded values of one {@link Client#getAttributes(String, AttributeRead...)} call.
 *
 * <p>{@code getAttributes} returns an {@code AttributeValues} only after every set value has
 * decoded. Look up each value with {@link #get(AttributeRead)}, passing the read that was part of
 * the call or an equal one. An {@link Attribute} matches only the same definition object. An
 * {@link AttributeMapInstance} matches any selector for the same {@link AttributeMap} object and
 * the same instance key, so a freshly created {@code notes.instance("n1")} works.
 *
 * <p>An unset Attribute or AttributeMap instance reads as {@link Optional#empty()}. Falsy values
 * such as an empty string, {@code 0}, and {@code false} are set values. A stored value that
 * decodes to {@code null} also reads as {@link Optional#empty()}, because {@link Optional} cannot
 * hold {@code null}. Writing {@code null} from a Step or RPC deletes the Attribute, so this case
 * is rare.
 *
 * <p>An {@code AttributeValues} cannot change after the call returns, and threads may share it.
 * Each {@link #get(AttributeRead)} call returns the same decoded value object for a read; the
 * value is not copied. Treat a mutable value as shared, or copy it before changing it.
 *
 * <pre>{@code
 * AttributeValues values = client.getAttributes(
 *         "order-42",
 *         orderFlow.orderStatus,
 *         orderFlow.notes.instance("n1"));
 * String status = values.get(orderFlow.orderStatus).orElse("new");
 * Optional<Note> note = values.get(orderFlow.notes.instance("n1"));
 * }</pre>
 */
public final class AttributeValues {
    private final String flowId;
    private final Map<String, ReadValue> valuesByKey;

    AttributeValues(final String flowId, final Map<String, ReadValue> valuesByKey) {
        this.flowId = flowId;
        this.valuesByKey = Collections.unmodifiableMap(valuesByKey);
    }

    /**
     * Returns the decoded value of one read from the call that produced these values.
     *
     * <p>The read must have been part of the {@code getAttributes} call. A read that was not part
     * of the call raises {@link IllegalArgumentException} rather than returning
     * {@link Optional#empty()}, so a typo or a wrong definition object is not mistaken for an unset
     * value. This includes another {@link Attribute} object with the same name, and an
     * {@link AttributeMapInstance} of another AttributeMap object or another instance key.
     *
     * <p>Repeated calls for the same read return the same decoded value object. The value is not
     * copied, so changes to a mutable value are visible to every caller that shares these values.
     *
     * <pre>{@code
     * AttributeValues values = client.getAttributes("order-42", orderFlow.orderStatus);
     * if (!values.get(orderFlow.orderStatus).isPresent()) {
     *     log.info("order-42 has no status yet");
     * }
     * }</pre>
     *
     * @param read the {@link Attribute} or {@link AttributeMapInstance} passed to
     *     {@code getAttributes}, or an equal {@link AttributeMapInstance}
     * @param <T> the Java value type of the read
     * @return the decoded value, or {@link Optional#empty()} when the Attribute or AttributeMap
     *     instance is not set or its stored value decodes to {@code null}
     * @throws NullPointerException if {@code read} is {@code null}
     * @throws IllegalArgumentException if {@code read} was not part of the call, or is not an
     *     {@link Attribute} or {@link AttributeMapInstance}; the message names the physical key and
     *     the Flow ID of the call
     */
    public <T> Optional<T> get(final AttributeRead<T> read) {
        Objects.requireNonNull(read, "read");
        final String key = physicalKey(read);
        final PersistenceDefinition definition = definition(read);
        final ReadValue value = valuesByKey.get(key);
        if (value == null || value.definition != definition) {
            throw new IllegalArgumentException(
                    "Attribute " + key + " was not read by this getAttributes call for Flow "
                            + flowId);
        }
        @SuppressWarnings("unchecked")
        final T decoded = (T) value.decoded;
        return Optional.ofNullable(decoded);
    }

    static String physicalKey(final AttributeRead<?> read) {
        if (read instanceof Attribute) {
            return ((Attribute<?>) read).getName();
        }
        if (read instanceof AttributeMapInstance) {
            return ((AttributeMapInstance<?>) read).getPhysicalName();
        }
        throw new IllegalArgumentException(
                "Attribute read must be an Attribute or an AttributeMapInstance, not "
                        + read.getClass().getName());
    }

    static PersistenceDefinition definition(final AttributeRead<?> read) {
        if (read instanceof Attribute) {
            return (Attribute<?>) read;
        }
        if (read instanceof AttributeMapInstance) {
            return ((AttributeMapInstance<?>) read).getAttributeMap();
        }
        throw new IllegalArgumentException(
                "Attribute read must be an Attribute or an AttributeMapInstance, not "
                        + read.getClass().getName());
    }

    static final class ReadValue {
        private final PersistenceDefinition definition;
        private final Object decoded;

        ReadValue(final PersistenceDefinition definition, final Object decoded) {
            this.definition = definition;
            this.decoded = decoded;
        }
    }
}
