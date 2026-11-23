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
 * <p>Set {@link Builder#requestId(String)} to identify a call that may be sent more than once.
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
    private final String requestId;

    private RPCInvokeOptions(final Builder builder) {
        lockAttributeMapInstances = immutable(builder.lockAttributeMapInstances);
        loadAttributeMapInstances = immutable(builder.loadAttributeMapInstances);
        loadChannelMapInstances = immutable(builder.loadChannelMapInstances);
        requestId = builder.requestId;
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

    String getRequestId() {
        return requestId;
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
        private String requestId;

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
         * Sets a stable request ID so Dex can deduplicate repeated attempts of one RPC invocation.
         *
         * <p>By default, the Client generates a new UUID on every call. Pass a stable ID when
         * retrying a call with an unknown outcome or processing an event delivered at least once.
         * Reuse an ID only for the same logical call. If one event triggers several RPCs on the
         * same Flow run, give each call its own ID, for example by appending the RPC name.
         *
         * <p>Dex records the request ID and reply when an RPC result commits. It applies the RPC's
         * Flow changes at most once while the request ID is retained.
         *
         * <ul>
         *   <li>A transactional or locking RPC returns the recorded reply before a Worker runs the
         *       handler. Concurrent calls with the same request ID share one execution and result.
         *       A recorded reply remains available after the Flow closes while Dex retains the run.
         *       A fresh request ID on that closed run yields
         *       {@link io.superdurable.dex.exceptions.FlowNotActiveOrNotFoundException}.
         *   <li>Any other RPC runs its handler again. If that attempt changes Flow state or records
         *       an event, Dex discards the changes and returns the recorded reply. An attempt that
         *       changes no Flow state and records no event returns its fresh output without checking
         *       or recording the request ID. Calls to external systems can repeat.
         * </ul>
         *
         * <p>Dex compares only the request ID when it checks for a repeat. A different RPC name,
         * input, or options under a recorded ID still count as a repeat. Dex Server 0.1.36 and
         * earlier compared the whole request and rejected a different request under a recorded ID
         * with a {@link io.superdurable.dex.exceptions.DexServiceException DexServiceException}
         * whose gRPC status is {@code INVALID_ARGUMENT}.
         *
         * <p>Each Flow run keeps its newest request IDs up to {@code limits.maxRequestIdsPerRun}
         * (1,000 by default). The run keeps the limit it started with. The window includes other
         * recorded Client requests on the run, such as Attribute writes. An evicted ID is treated
         * as a new request. A new run starts with an empty window. A run over its Flow state
         * budget may omit the ID and reply of a transactional or locking RPC that makes no Flow
         * changes. Committed RPCs that change Flow state still keep their ID and reply.
         *
         * <p>A server-side failure before commit, such as a Worker error or an RPC handler timeout,
         * records nothing. A caller-side deadline or cancellation, a lost reply, or an
         * {@code UNAVAILABLE} error during commit can leave the outcome unknown. Retry with the
         * same ID. An in-flight transactional or locking call can still commit after the caller
         * leaves. Its retry joins the call in flight or returns its recorded reply once it commits.
         *
         * <pre>{@code
         * static int deliverCounterEvent(Client client, String flowId, String runId,
         *                                String eventId, int amount) {
         *     CounterFlow rpc = client.newRpcStub(CounterFlow.class, flowId, runId);
         *     RPCInvokeOptions options = RPCInvokeOptions.newBuilder()
         *             .requestId(eventId + "/incrementCounter")
         *             .build();
         *     // incrementCounter is declared with @RPC(isTransactional = true).
         *     return client.invokeRPC(rpc::incrementCounter, amount, options);
         * }
         * }</pre>
         *
         * @param requestId the non-empty ID for this logical call, or {@code null} to generate an ID
         * @return this builder
         * @throws IllegalArgumentException if {@code requestId} is empty
         * @since 1.45.7
         */
        public Builder requestId(final String requestId) {
            if (requestId != null && requestId.isEmpty()) {
                throw new IllegalArgumentException("RPC request ID must not be empty");
            }
            this.requestId = requestId;
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
