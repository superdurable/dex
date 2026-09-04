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

import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

/**
 * Configures one durable SubFlow condition.
 *
 * <p>The server generates the SubFlow Flow ID and request ID. The SubFlow inherits the parent's
 * effective {@link FlowConfig}; {@link Builder#configOverride} replaces only fields explicitly
 * present in its argument. Other unset start settings use normal Flow start defaults. The default
 * reuse policy is
 * {@link SubFlowReusePolicy#RESTART_IF_PREVIOUS_EXITS_ABNORMALLY}.
 *
 * <pre>{@code
 * SubFlowOptions options = SubFlowOptions.newBuilder()
 *         .timeout(Duration.ofMinutes(20))
 *         .reusePolicy(SubFlowReusePolicy.ATTACH)
 *         .conditionId("inventory")
 *         .build();
 * return Wait.until(SubFlow.run(InventoryFlow.class, input, options));
 * }</pre>
 */
public final class SubFlowOptions {
    private final Duration timeout;
    private final FlowTimeoutPolicy timeoutPolicy;
    private final FlowTimeoutHandlerOptions timeoutHandlerOptions;
    private final Duration startDelay;
    private final RetryPolicy retryPolicy;
    private final List<AttributeInitialization> attributes;
    private final FlowConfig configOverride;
    private final SubFlowReusePolicy reusePolicy;
    private final String conditionId;

    private SubFlowOptions(final Builder builder) {
        timeout = builder.timeout;
        timeoutPolicy = builder.timeoutPolicy;
        timeoutHandlerOptions = builder.timeoutHandlerOptions;
        startDelay = builder.startDelay;
        retryPolicy = builder.retryPolicy;
        attributes = Collections.unmodifiableList(
                new ArrayList<AttributeInitialization>(builder.attributes));
        configOverride = builder.configOverride;
        reusePolicy = builder.reusePolicy;
        conditionId = builder.conditionId;
    }

    /**
     * Returns a builder initialized with SubFlow defaults.
     *
     * @return a new mutable builder
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    Duration getTimeout() {
        return timeout;
    }

    FlowTimeoutPolicy getTimeoutPolicy() {
        return timeoutPolicy;
    }

    FlowTimeoutHandlerOptions getTimeoutHandlerOptions() {
        return timeoutHandlerOptions;
    }

    Duration getStartDelay() {
        return startDelay;
    }

    RetryPolicy getRetryPolicy() {
        return retryPolicy;
    }

    List<AttributeInitialization> getAttributes() {
        return attributes;
    }

    FlowConfig getConfigOverride() {
        return configOverride;
    }

    SubFlowReusePolicy getReusePolicy() {
        return reusePolicy;
    }

    String getConditionId() {
        return conditionId;
    }

    /**
     * Builds immutable SubFlow options.
     *
     * <p>A builder can be reused; each built value owns an immutable copy of its Attribute
     * initializations.
     */
    public static final class Builder {
        private Duration timeout;
        private FlowTimeoutPolicy timeoutPolicy = FlowTimeoutPolicy.DEFAULT;
        private FlowTimeoutHandlerOptions timeoutHandlerOptions;
        private Duration startDelay;
        private RetryPolicy retryPolicy;
        private final List<AttributeInitialization> attributes =
                new ArrayList<AttributeInitialization>();
        private FlowConfig configOverride;
        private SubFlowReusePolicy reusePolicy =
                SubFlowReusePolicy.RESTART_IF_PREVIOUS_EXITS_ABNORMALLY;
        private String conditionId;

        private Builder() {
        }

        /**
         * Sets Dex's durable soft timeout for the SubFlow execution.
         *
         * @param value a nonnegative whole-second duration, or {@code null} for no explicit timeout
         * @return this builder
         * @throws IllegalArgumentException during condition mapping if the duration is unsupported
         */
        public Builder timeout(final Duration value) {
            timeout = value;
            return this;
        }

        /**
         * Sets the action taken when the positive SubFlow timeout expires.
         *
         * <p>The default invokes the target Flow's overridden {@link Flow#handleTimeout}; otherwise
         * it fails the SubFlow. HANDLER requires that override.
         *
         * @param value the non-null timeout policy
         * @return this builder
         * @throws NullPointerException if {@code value} is {@code null}
         */
        public Builder timeoutPolicy(final FlowTimeoutPolicy value) {
            timeoutPolicy = Objects.requireNonNull(value, "timeoutPolicy");
            return this;
        }

        /**
         * Configures timeout-handler execution and selective state loading.
         *
         * <p>This option requires a positive timeout and a policy resolving to HANDLER.
         *
         * @param value the handler options, or {@code null} for server defaults
         * @return this builder
         */
        public Builder timeoutHandlerOptions(final FlowTimeoutHandlerOptions value) {
            timeoutHandlerOptions = value;
            return this;
        }

        /**
         * Sets the delay before the SubFlow starts.
         *
         * @param value a nonnegative whole-second duration, or {@code null} for no explicit delay
         * @return this builder
         * @throws IllegalArgumentException during condition mapping if the duration is unsupported
         */
        public Builder startDelay(final Duration value) {
            startDelay = value;
            return this;
        }

        /**
         * Sets retry behavior for abnormal SubFlow completion.
         *
         * @param value the Flow retry policy, or {@code null} for no explicit policy
         * @return this builder
         */
        public Builder retryPolicy(final RetryPolicy value) {
            retryPolicy = value;
            return this;
        }

        /**
         * Adds one initial static Attribute value to the SubFlow start.
         *
         * @param attribute an Attribute registered by the target SubFlow
         * @param value the initial value, including {@code null} when supported by its value type
         * @param <T> the Attribute value type
         * @return this builder
         * @throws NullPointerException if {@code attribute} is {@code null}
         */
        public <T> Builder addAttribute(final Attribute<T> attribute, final T value) {
            attributes.add(new AttributeInitialization(
                    Objects.requireNonNull(attribute, "attribute"), null, value));
            return this;
        }

        /**
         * Adds one initial Attribute-map value to the SubFlow start.
         *
         * @param attribute an Attribute map registered by the target SubFlow
         * @param instance the map instance. Slash is prohibited because it is a reserved character
         * @param value the initial value, including {@code null} when supported by its value type
         * @param <T> the Attribute value type
         * @return this builder
         * @throws NullPointerException if {@code attribute} is {@code null}
         * @throws IllegalArgumentException if {@code instance} is blank or contains {@code /}
         */
        public <T> Builder addAttribute(
                final AttributeMap<T> attribute,
                final String instance,
                final T value) {
            attributes.add(new AttributeInitialization(
                    Objects.requireNonNull(attribute, "attribute"),
                    Attribute.requireMapInstance(instance),
                    value));
            return this;
        }

        /**
         * Overrides fields in the parent Flow's effective configuration for the SubFlow.
         *
         * @param value the partial Flow configuration, or {@code null} for no overrides
         * @return this builder
         */
        public Builder configOverride(final FlowConfig value) {
            configOverride = value;
            return this;
        }

        /**
         * Sets how an execution already using the generated Flow ID is resolved.
         *
         * @param value the non-null reuse policy
         * @return this builder
         * @throws NullPointerException if {@code value} is {@code null}
         */
        public Builder reusePolicy(final SubFlowReusePolicy value) {
            reusePolicy = Objects.requireNonNull(value, "reusePolicy");
            return this;
        }

        /**
         * Sets the condition ID required by {@link Wait#anyCombinationOf}.
         *
         * @param value the stable condition ID, or {@code null} outside any-combination waits
         * @return this builder
         * @throws io.superdurable.dex.exceptions.InvalidStepResultException during condition
         *     mapping if the ID is empty or duplicates another condition
         */
        public Builder conditionId(final String value) {
            conditionId = value;
            return this;
        }

        /**
         * Builds immutable options.
         *
         * @return a new immutable options value
         */
        public SubFlowOptions build() {
            return new SubFlowOptions(this);
        }
    }

    static final class AttributeInitialization {
        private final PersistenceDefinition definition;
        private final String instance;
        private final Object value;

        AttributeInitialization(
                final PersistenceDefinition definition,
                final String instance,
                final Object value) {
            this.definition = definition;
            this.instance = instance;
            this.value = value;
        }

        PersistenceDefinition getDefinition() {
            return definition;
        }

        String getInstance() {
            return instance;
        }

        Object getValue() {
            return value;
        }
    }
}
