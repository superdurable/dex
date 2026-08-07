/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex;

import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

public final class StartFlowOptions {
    private final Duration timeout;
    private final Duration startDelay;
    private final IdReusePolicy idReusePolicy;
    private final String cronSchedule;
    private final RetryPolicy retryPolicy;
    private final List<AttributeInitialization> attributes;
    private final FlowConfig configOverride;
    private final boolean ignoreAlreadyStarted;
    private final String requestId;

    public StartFlowOptions() {
        this(new Builder());
    }

    private StartFlowOptions(final Builder builder) {
        this.timeout = builder.timeout;
        this.startDelay = builder.startDelay;
        this.idReusePolicy = builder.idReusePolicy;
        this.cronSchedule = builder.cronSchedule;
        this.retryPolicy = builder.retryPolicy;
        this.attributes = Collections.unmodifiableList(
                new ArrayList<AttributeInitialization>(builder.attributes));
        this.configOverride = builder.configOverride;
        this.ignoreAlreadyStarted = builder.ignoreAlreadyStarted;
        this.requestId = builder.requestId;
    }

    public static Builder newBuilder() {
        return new Builder();
    }

    Duration getTimeout() {
        return timeout;
    }

    Duration getStartDelay() {
        return startDelay;
    }

    IdReusePolicy getIdReusePolicy() {
        return idReusePolicy;
    }

    String getCronSchedule() {
        return cronSchedule;
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

    boolean isIgnoreAlreadyStarted() {
        return ignoreAlreadyStarted;
    }

    String getRequestId() {
        return requestId;
    }

    public static final class Builder {
        private Duration timeout;
        private Duration startDelay;
        private IdReusePolicy idReusePolicy = IdReusePolicy.DEFAULT;
        private String cronSchedule;
        private RetryPolicy retryPolicy;
        private final List<AttributeInitialization> attributes =
                new ArrayList<AttributeInitialization>();
        private FlowConfig configOverride;
        private boolean ignoreAlreadyStarted;
        private String requestId;

        private Builder() {
        }

        public Builder timeout(final Duration value) {
            timeout = value;
            return this;
        }

        public Builder startDelay(final Duration value) {
            startDelay = value;
            return this;
        }

        public Builder idReusePolicy(final IdReusePolicy value) {
            idReusePolicy = value;
            return this;
        }

        public Builder cronSchedule(final String value) {
            cronSchedule = value;
            return this;
        }

        public Builder retryPolicy(final RetryPolicy value) {
            retryPolicy = value;
            return this;
        }

        public <T> Builder addAttribute(final Attribute<T> attribute, final T value) {
            attributes.add(new AttributeInitialization(
                    Objects.requireNonNull(attribute, "attribute"),
                    null,
                    value));
            return this;
        }

        public <T> Builder addAttribute(
                final AttributeMap<T> attributeMap,
                final String instance,
                final T value) {
            attributes.add(new AttributeInitialization(
                    Objects.requireNonNull(attributeMap, "attributeMap"),
                    Attribute.requireName(instance),
                    value));
            return this;
        }

        public Builder configOverride(final FlowConfig value) {
            configOverride = value;
            return this;
        }

        public Builder ignoreAlreadyStarted(final boolean value) {
            ignoreAlreadyStarted = value;
            return this;
        }

        public Builder requestId(final String value) {
            requestId = value;
            return this;
        }

        public StartFlowOptions build() {
            return new StartFlowOptions(this);
        }
    }

    static final class AttributeInitialization {
        private final PersistenceDefinition definition;
        private final String instance;
        private final Object value;

        private AttributeInitialization(
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
