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
 * Selects one typed AttributeMap instance for a Client Attribute read.
 *
 * <p>Create a selector with {@link AttributeMap#instance(String)}. Pass it to
 * {@link Client#getAttributes(String, AttributeRead...)} together with singleton Attributes. Then
 * pass it, or an equal selector, to {@link AttributeValues#get(AttributeRead)} to obtain the typed
 * value. Selectors are immutable and safe to share between threads. A selector holds no value.
 * {@link AttributeValues#get(AttributeRead)} returns the same decoded value object each time it is
 * called with an equal selector, and it does not copy that value.
 *
 * <p>Two selectors are equal when they refer to the same {@link AttributeMap} object and their
 * instance keys are equal strings. Calling {@code instance("n1")} twice on the same AttributeMap
 * therefore yields equal selectors. An AttributeMap object created by another
 * {@link AttributeMap#define} call is a different definition, even when its name and value type
 * are the same.
 *
 * <pre>{@code
 * AttributeMap<Note> notes = orderFlow.notes;
 * AttributeValues values = client.getAttributes("order-42", notes.instance("n1"));
 * Optional<Note> firstNote = values.get(notes.instance("n1"));
 * }</pre>
 *
 * @param <T> the Java value type stored in every instance of the AttributeMap
 */
public final class AttributeMapInstance<T> implements AttributeRead<T> {
    private final AttributeMap<T> attributeMap;
    private final String instance;
    private final String physicalName;

    AttributeMapInstance(final AttributeMap<T> attributeMap, final String instance) {
        this.attributeMap = attributeMap;
        this.instance = instance;
        this.physicalName = physicalName(attributeMap, instance);
    }

    private static String physicalName(final AttributeMap<?> attributeMap, final String instance) {
        if (instance == null || instance.trim().isEmpty()) {
            throw new IllegalArgumentException(
                    "AttributeMap " + attributeMap.getName() + " instance key must not be blank");
        }
        try {
            return Registry.physicalName(attributeMap.getName(), instance);
        } catch (IllegalArgumentException invalid) {
            throw new IllegalArgumentException(
                    "AttributeMap " + attributeMap.getName() + " instance key is invalid: "
                            + invalid.getMessage(),
                    invalid);
        }
    }

    /**
     * Returns the AttributeMap definition that owns the selected instance.
     *
     * @return the AttributeMap object passed to {@link AttributeMap#instance(String)}
     */
    public AttributeMap<T> getAttributeMap() {
        return attributeMap;
    }

    /**
     * Returns the logical instance key.
     *
     * @return the non-blank instance key, which contains no {@code /}
     */
    public String getInstance() {
        return instance;
    }

    String getPhysicalName() {
        return physicalName;
    }

    /**
     * Compares this selector with another object.
     *
     * @param other the object to compare, which may be {@code null}
     * @return {@code true} when {@code other} is an AttributeMapInstance for the same AttributeMap
     *     object and an equal instance key
     */
    @Override
    public boolean equals(final Object other) {
        if (this == other) {
            return true;
        }
        if (!(other instanceof AttributeMapInstance)) {
            return false;
        }
        final AttributeMapInstance<?> selector = (AttributeMapInstance<?>) other;
        return attributeMap == selector.attributeMap && instance.equals(selector.instance);
    }

    /**
     * Returns a hash code consistent with {@link #equals(Object)}.
     *
     * @return the hash code of the AttributeMap object identity and the instance key
     */
    @Override
    public int hashCode() {
        return 31 * System.identityHashCode(attributeMap) + instance.hashCode();
    }

    /**
     * Returns the physical Attribute key of the selected instance.
     *
     * @return the AttributeMap name, a {@code /}, and the URL-encoded instance key
     */
    @Override
    public String toString() {
        return physicalName;
    }
}
