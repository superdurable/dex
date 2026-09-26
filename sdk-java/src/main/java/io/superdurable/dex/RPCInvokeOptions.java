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

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

/**
 * Adds runtime-selected Attribute-map locks and exact map-instance loads to one RPC invocation.
 *
 * <p>These selections are additive. Dex unions them with the locks and state loads declared by the
 * RPC annotation, then sorts and deduplicates the physical names. A read-modify-write handler must
 * add both a lock and an Attribute-map load for the same instance.
 *
 * <pre>{@code
 * RPCInvokeOptions options = RPCInvokeOptions.newBuilder()
 *         .addLockAttributeMapInstance(customerProfiles, partitionName)
 *         .addLoadAttributeMapInstance(customerProfiles, partitionName)
 *         .build();
 * client.invokeRPC(directoryRpc::upsertCustomerProfile, profile, options);
 * }</pre>
 */
public final class RPCInvokeOptions {
    private final List<MapInstance> lockAttributeMapInstances;
    private final List<MapInstance> loadAttributeMapInstances;
    private final List<MapInstance> loadChannelMapInstances;

    private RPCInvokeOptions(final Builder builder) {
        lockAttributeMapInstances = immutable(builder.lockAttributeMapInstances);
        loadAttributeMapInstances = immutable(builder.loadAttributeMapInstances);
        loadChannelMapInstances = immutable(builder.loadChannelMapInstances);
    }

    /**
     * Creates an empty builder.
     *
     * @return a mutable builder with no invocation-time selections
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    List<MapInstance> getLockAttributeMapInstances() {
        return lockAttributeMapInstances;
    }

    List<MapInstance> getLoadAttributeMapInstances() {
        return loadAttributeMapInstances;
    }

    List<MapInstance> getLoadChannelMapInstances() {
        return loadChannelMapInstances;
    }

    private static List<MapInstance> immutable(final List<MapInstance> values) {
        return Collections.unmodifiableList(new ArrayList<MapInstance>(values));
    }

    /** Builds immutable {@link RPCInvokeOptions} values. */
    public static final class Builder {
        private final List<MapInstance> lockAttributeMapInstances =
                new ArrayList<MapInstance>();
        private final List<MapInstance> loadAttributeMapInstances =
                new ArrayList<MapInstance>();
        private final List<MapInstance> loadChannelMapInstances =
                new ArrayList<MapInstance>();

        private Builder() {
        }

        /**
         * Adds an invocation-time lock for one Attribute-map instance.
         *
         * <p>The lock does not load the instance. Add the same instance through
         * {@link #addLoadAttributeMapInstance} when the handler reads or writes its current value.
         *
         * @param definition the Attribute map registered by the RPC's Flow
         * @param instance the nonblank instance name; slash is prohibited
         * @return this builder
         * @throws NullPointerException if {@code definition} is {@code null}
         * @throws IllegalArgumentException if {@code instance} is invalid
         */
        public Builder addLockAttributeMapInstance(
                final AttributeMap<?> definition,
                final String instance) {
            lockAttributeMapInstances.add(new MapInstance(definition, instance));
            return this;
        }

        /**
         * Adds an exact Attribute-map instance load for one RPC invocation.
         *
         * @param definition the Attribute map registered by the RPC's Flow
         * @param instance the nonblank instance name; slash is prohibited
         * @return this builder
         * @throws NullPointerException if {@code definition} is {@code null}
         * @throws IllegalArgumentException if {@code instance} is invalid
         */
        public Builder addLoadAttributeMapInstance(
                final AttributeMap<?> definition,
                final String instance) {
            loadAttributeMapInstances.add(new MapInstance(definition, instance));
            return this;
        }

        /**
         * Adds an exact Channel-map instance load for one RPC invocation.
         *
         * @param definition the Channel map registered by the RPC's Flow
         * @param instance the nonblank instance name; slash is prohibited
         * @return this builder
         * @throws NullPointerException if {@code definition} is {@code null}
         * @throws IllegalArgumentException if {@code instance} is invalid
         */
        public Builder addLoadChannelMapInstance(
                final ChannelMap<?> definition,
                final String instance) {
            loadChannelMapInstances.add(new MapInstance(definition, instance));
            return this;
        }

        /**
         * Creates immutable invocation options.
         *
         * @return the configured options
         */
        public RPCInvokeOptions build() {
            return new RPCInvokeOptions(this);
        }
    }

    static final class MapInstance {
        private final PersistenceDefinition definition;
        private final String instance;

        private MapInstance(
                final PersistenceDefinition definition,
                final String instance) {
            this.definition = Objects.requireNonNull(definition, "definition");
            this.instance = Attribute.requireMapInstance(instance);
        }

        PersistenceDefinition getDefinition() {
            return definition;
        }

        String getInstance() {
            return instance;
        }
    }
}
