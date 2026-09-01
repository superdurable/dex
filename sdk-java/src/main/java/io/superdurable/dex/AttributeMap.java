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
 * Defines a durable map of named, typed Attribute instances.
 *
 * <p>An {@code AttributeMap} is useful when keys are dynamically determined at runtime. Register
 * the definition once in the Flow's {@link PersistenceSchema}, then supply an instance name for
 * each read, write, delete, or lock. Each key/value pair is stored as a separate blob, so updating
 * one entry does not rewrite a large Attribute containing the entire logical map. Each value is
 * serialized with the definition's concrete {@link Class}.
 *
 * <pre>{@code
 * private final AttributeMap<String> orderStatus =
 *         AttributeMap.define("order-status", String.class);
 *
 * orderStatus.set(context, orderId, "PAID");
 * String status = orderStatus.get(context, orderId);
 * }</pre>
 *
 * @param <T> the Java value type stored in every map instance
 */
public final class AttributeMap<T> extends PersistenceDefinition {
    private final String name;
    private final Class<T> valueType;
    private final AttributeIndex index;
    private final boolean syncToAttributeStore;

    private AttributeMap(
            final String name,
            final Class<T> valueType,
            final AttributeIndex index,
            final boolean syncToAttributeStore) {
        this.name = Attribute.requirePersistenceDefinitionName(name);
        this.valueType = Objects.requireNonNull(valueType, "valueType");
        this.index = index;
        this.syncToAttributeStore = syncToAttributeStore;
    }

    /**
     * Defines an Attribute map without a search index.
     *
     * @param name the stable Attribute-map name; must not be blank or contain {@code /}
     * @param valueType the concrete Java class used for every instance value
     * @param <T> the Java value type
     * @return the typed Attribute-map definition
     * @throws IllegalArgumentException if {@code name} is {@code null}, blank, or contains {@code /}
     * @throws NullPointerException if {@code valueType} is {@code null}
     */
    public static <T> AttributeMap<T> define(final String name, final Class<T> valueType) {
        return new AttributeMap<T>(name, valueType, null, false);
    }

    /**
     * Defines an Attribute map with a search index.
     *
     * @param name the stable Attribute-map name; must not be blank or contain {@code /}
     * @param valueType the concrete Java class used for every instance value
     * @param index the search-index definition, or {@code null} for no index
     * @param <T> the Java value type
     * @return the typed Attribute-map definition
     * @throws IllegalArgumentException if {@code name} is {@code null}, blank, or contains {@code /}
     * @throws NullPointerException if {@code valueType} is {@code null}
     */
    public static <T> AttributeMap<T> define(
            final String name,
            final Class<T> valueType,
            final AttributeIndex index) {
        return new AttributeMap<T>(name, valueType, index, false);
    }

    /**
     * Returns a definition whose instance writes are projected through the Flow's Attribute Store.
     *
     * <p>The physical Attribute-map key must match a target column. Projection is asynchronous and
     * does not roll back the Flow Attribute when external storage fails. Deletion projects SQL
     * {@code NULL} when the target column permits it.
     *
     * @return a new immutable Attribute-map definition with Attribute Store synchronization enabled
     */
    public AttributeMap<T> syncToAttributeStore() {
        return new AttributeMap<T>(name, valueType, index, true);
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
     * Reads one map instance from the current invocation.
     *
     * @param context the Step or RPC invocation context
     * @param instance the instance key within this map
     * @return the current typed value, or {@code null} when the instance is absent
     */
    public T get(final Context context, final String instance) {
        return context.getAttribute(this, instance);
    }

    /**
     * Writes one map instance in the current invocation.
     *
     * @param context the Step or RPC invocation context
     * @param instance the instance key within this map
     * @param value the typed value to persist
     */
    public void set(final Context context, final String instance, final T value) {
        context.setAttribute(this, instance, value);
    }

    /**
     * Deletes one map instance in the current invocation.
     *
     * @param context the Step or RPC invocation context
     * @param instance the instance key within this map
     */
    public void delete(final Context context, final String instance) {
        context.deleteAttribute(this, instance);
    }

    /**
     * Returns the number of existing instances, including writes buffered by this invocation.
     *
     * @param context the Step or RPC invocation context
     * @return the current number of instances
     */
    public int getMapSize(final Context context) {
        return getAllInstanceKeys(context).size();
    }

    /**
     * Returns existing instance keys in ascending order, including buffered writes.
     *
     * @param context the Step or RPC invocation context
     * @return an immutable sorted list of decoded instance keys
     */
    public java.util.List<String> getAllInstanceKeys(final Context context) {
        if (!(context instanceof InvocationContext)) {
            throw new IllegalArgumentException("Dex invocation Context is required");
        }
        return ((InvocationContext) context).attributeMapKeys(this);
    }
}
