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

import java.util.Objects;

/**
 * Defines one durable, typed value stored with a Flow execution.
 *
 * <p>Declare Attributes once on the Flow, include them in its {@link PersistenceSchema}, and use
 * the same object from Step and RPC code. Values are serialized using the {@link Class} supplied at
 * definition time. A parameterized value type such as {@code List<String>} cannot be supplied
 * directly because {@code List.class} does not retain its element type. Wrap the value in a
 * concrete class whose field is a {@code List<String>}, or use {@code String[]} when an array fits
 * the data model.
 *
 * <pre>{@code
 * private final Attribute<Integer> counter =
 *         Attribute.define("counter", Integer.class);
 * private final Attribute<Tags> tags =
 *         Attribute.define("tags", Tags.class);
 * private final Attribute<String[]> tagArray =
 *         Attribute.define("tag-array", String[].class);
 *
 * static final class Tags {
 *     public List<String> values;
 * }
 *
 * public StepDecision execute(Context context, Command input) {
 *     counter.set(context, counter.get(context) + 1);
 *     return StepDecision.gracefulComplete();
 * }
 * }</pre>
 *
 * @param <T> the Java value type stored in the Attribute
 */
public final class Attribute<T> extends PersistenceDefinition {
    private final String name;
    private final Class<T> valueType;
    private final AttributeIndex index;
    private final boolean syncToAttributeStore;

    private Attribute(
            final String name,
            final Class<T> valueType,
            final AttributeIndex index,
            final boolean syncToAttributeStore) {
        this.name = requirePersistenceDefinitionName(name);
        this.valueType = Objects.requireNonNull(valueType, "valueType");
        this.index = index;
        this.syncToAttributeStore = syncToAttributeStore;
    }

    /**
     * Defines an Attribute without a search index.
     *
     * @param name the stable Attribute name; must not be blank or contain {@code /}
     * @param valueType the concrete Java class used to serialize and deserialize values
     * @param <T> the Java value type
     * @return the typed Attribute definition
     * @throws IllegalArgumentException if {@code name} is {@code null}, blank, or contains {@code /}
     * @throws NullPointerException if {@code valueType} is {@code null}
     */
    public static <T> Attribute<T> define(final String name, final Class<T> valueType) {
        return new Attribute<T>(name, valueType, null, false);
    }

    /**
     * Defines an Attribute with a search index.
     *
     * @param name the stable Attribute name; must not be blank or contain {@code /}
     * @param valueType the concrete Java class used to serialize and deserialize values
     * @param index the search-index definition, or {@code null} for no index
     * @param <T> the Java value type
     * @return the typed Attribute definition
     * @throws IllegalArgumentException if {@code name} is {@code null}, blank, or contains {@code /}
     * @throws NullPointerException if {@code valueType} is {@code null}
     */
    public static <T> Attribute<T> define(
            final String name,
            final Class<T> valueType,
            final AttributeIndex index) {
        return new Attribute<T>(name, valueType, index, false);
    }

    /**
     * Returns a definition whose writes are projected through the Flow's Attribute Store.
     *
     * <p>The projection is asynchronous and latest-state only. A projection failure does not roll
     * back the Flow Attribute. Deleting this Attribute projects SQL {@code NULL} when the target
     * column permits it.
     *
     * @return a new immutable Attribute definition with Attribute Store synchronization enabled
     */
    public Attribute<T> syncToAttributeStore() {
        return new Attribute<T>(name, valueType, index, true);
    }

    String getName() {
        return name;
    }

    Class<T> getValueType() {
        return valueType;
    }

    AttributeIndex getIndex() {
        return index;
    }

    @Override
    boolean isSyncToAttributeStore() {
        return syncToAttributeStore;
    }

    /**
     * Reads this Attribute from the current invocation.
     *
     * @param context the Step or RPC invocation context
     * @return the current typed value, or {@code null} when no value is stored
     */
    public T get(final Context context) {
        return context.getAttribute(this);
    }

    /**
     * Writes this Attribute in the current invocation.
     *
     * @param context the Step or RPC invocation context
     * @param value the typed value to persist
     */
    public void set(final Context context, final T value) {
        context.setAttribute(this, value);
    }

    /**
     * Deletes this Attribute in the current invocation.
     *
     * @param context the Step or RPC invocation context
     */
    public void delete(final Context context) {
        context.deleteAttribute(this);
    }

    static String requireName(final String name) {
        if (name == null || name.trim().isEmpty()) {
            throw new IllegalArgumentException("name is required");
        }
        return name;
    }

    static String requirePersistenceDefinitionName(final String name) {
        final String value = requireName(name);
        if (value.contains("/")) {
            throw new IllegalArgumentException("persistence definition names must not contain '/'");
        }
        return value;
    }
}
