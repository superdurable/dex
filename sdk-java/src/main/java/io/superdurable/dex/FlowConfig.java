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
import java.util.Arrays;
import java.util.Collections;
import java.util.List;

/**
 * Configures server behavior for one Flow execution.
 *
 * <p>Use this value as a start-time override or in an update request.
 * Flow starts select pull Workers; updates preserve the existing Worker routing.
 * Unset properties use server defaults at start and retain existing values on update.
 * Continue-as-new thresholds bound execution history and page size; consult the server deployment
 * configuration before overriding them.
 *
 * <pre>{@code
 * FlowConfig config = FlowConfig.newBuilder()
 *         .activeStepSearchMode(ActiveStepSearchMode.WITH_WAIT_FOR)
 *         .stepDurability(StepDurability.SYNC)
 *         .historyWindowEvents(50_000)
 *         .build();
 * }</pre>
 */
public final class FlowConfig {
    private final ActiveStepSearchMode activeStepSearchMode;
    private final Integer continueAsNewThreshold;
    private final Integer continueAsNewPageSizeBytes;
    private final StepDurability stepDurability;
    private final List<String> attributeStoreNames;
    private final Integer historyWindowEvents;
    private final Boolean deleteExpiredHistoryObjects;

    private FlowConfig(final Builder builder) {
        this.activeStepSearchMode = builder.activeStepSearchMode;
        this.continueAsNewThreshold = builder.continueAsNewThreshold;
        this.continueAsNewPageSizeBytes = builder.continueAsNewPageSizeBytes;
        this.stepDurability = builder.stepDurability;
        this.attributeStoreNames = builder.attributeStoreNames;
        this.historyWindowEvents = builder.historyWindowEvents;
        this.deleteExpiredHistoryObjects = builder.deleteExpiredHistoryObjects;
    }

    /**
     * Creates a builder whose unset values use server defaults.
     *
     * @return a new mutable builder
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    ActiveStepSearchMode getActiveStepSearchMode() {
        return activeStepSearchMode;
    }

    Integer getContinueAsNewThreshold() {
        return continueAsNewThreshold;
    }

    Integer getContinueAsNewPageSizeBytes() {
        return continueAsNewPageSizeBytes;
    }

    StepDurability getStepDurability() {
        return stepDurability;
    }

    List<String> getAttributeStoreNames() {
        return attributeStoreNames;
    }

    Boolean getDeleteExpiredHistoryObjects() {
        return deleteExpiredHistoryObjects;
    }

    Integer getHistoryWindowEvents() {
        return historyWindowEvents;
    }

    /** Builds immutable {@link FlowConfig} values. */
    public static final class Builder {
        private ActiveStepSearchMode activeStepSearchMode;
        private Integer continueAsNewThreshold;
        private Integer continueAsNewPageSizeBytes;
        private StepDurability stepDurability;
        private List<String> attributeStoreNames;
        private Integer historyWindowEvents;
        private Boolean deleteExpiredHistoryObjects;

        private Builder() {
        }

        /**
         * Sets how precisely Dex indexes active Step types for later searches.
         *
         * <p>The selected mode controls updates to the {@code ActiveStepTypes} search index. Use
         * that index to find running Flows before renaming or removing a Step implementation.
         *
         * @param value the active-Step indexing mode, or {@code null} for the server default
         * @return this builder
         */
        public Builder activeStepSearchMode(final ActiveStepSearchMode value) {
            activeStepSearchMode = value;
            return this;
        }

        /**
         * Sets the history-event threshold that triggers continue-as-new.
         *
         * @param value the threshold count
         * @return this builder
         */
        public Builder continueAsNewThreshold(final int value) {
            continueAsNewThreshold = value;
            return this;
        }

        /**
         * Sets the history page-size threshold that triggers continue-as-new.
         *
         * @param value the page-size threshold in bytes
         * @return this builder
         */
        public Builder continueAsNewPageSizeBytes(final int value) {
            continueAsNewPageSizeBytes = value;
            return this;
        }

        /**
         * Sets the normal default durability for Step methods in this Flow.
         *
         * <p>The server default is {@link StepDurability#SYNC}. A method-level setting in
         * {@link StepOptions} takes precedence over this Flow-wide value. Failure policies do not
         * change that ordering or the selected default.
         *
         * @param value the durability mode, or {@code null} for the server default
         * @return this builder
         */
        public Builder stepDurability(final StepDurability value) {
            stepDurability = value;
            return this;
        }

        /**
         * Selects Server-configured Attribute Stores for enabled Attribute writes.
         *
         * <p>Each enabled Attribute write is projected to every selected store. An empty list
         * explicitly disables future projections, while omitting this call leaves the current or
         * server-selected values unchanged. Already queued projections retain their original targets.
         *
         * @param value the Attribute Store names, an empty list to disable future projections, or
         *     {@code null} for the server default
         * @return this builder
         */
        public Builder attributeStoreNames(final List<String> value) {
            attributeStoreNames = value == null
                    ? null
                    : Collections.unmodifiableList(new ArrayList<>(value));
            return this;
        }

        /**
         * Selects Server-configured Attribute Stores for enabled Attribute writes.
         *
         * <p>This overload is convenient when a Flow uses one or a few stores. Each enabled
         * Attribute write is projected to every supplied store. Calling this method with no names
         * explicitly disables future projections; omitting it leaves the current or server-selected
         * values unchanged. Already queued projections retain their original targets.
         *
         * <pre>{@code
         * FlowConfig config = FlowConfig.newBuilder()
         *         .attributeStoreNames("profiles")
         *         .build();
         * }</pre>
         *
         * @param values the Attribute Store names; no names disables future projections
         * @return this builder
         */
        public Builder attributeStoreNames(final String... values) {
            return attributeStoreNames(values == null ? null : Arrays.asList(values));
        }

        /**
         * Sets about how many of the newest history events the run keeps for history reads and
         * Time Travel.
         *
         * <p>Omitting this call uses the server's {@code history.windowEvents} setting, which
         * defaults to 10000. Raise it for a run that you inspect or travel back in often. Lower it
         * for a run that writes many events and needs less disk.
         *
         * <p>Dex prunes the oldest events in chunks, and only between commands, so a run keeps
         * slightly more or fewer events than the value. A command that changes the Flow but records
         * no event counts as one event. The first event of the run is always kept, so a reset to the
         * beginning of the run always works. History reads no longer return pruned events, and Time
         * Travel targets only commands inside the window.
         *
         * <p>Set it at start through {@link StartFlowOptions.Builder#configOverride(FlowConfig)}, or
         * change it on an active Flow with {@link Client#updateFlowConfig(String, FlowConfig)}. A
         * smaller value takes effect at the run's next pruning. A larger value keeps more of the
         * events recorded afterwards; pruned events never come back. Once set, the value can be
         * changed but not cleared back to the server default: an update that omits this call keeps
         * the current value. A SubFlow inherits the value from its parent unless its own
         * {@link SubFlowOptions} override sets it.
         *
         * <p>The value must be positive and at most the server's {@code history.maxWindowEvents}
         * setting, which defaults to 100000. Starting or updating a Flow with any other value
         * throws {@link io.superdurable.dex.exceptions.DexServiceException} whose
         * {@code getCode()} is {@code INVALID_ARGUMENT}. A Step that starts a SubFlow with such an
         * override is an invalid Worker response: the Step attempt fails and Dex retries it under
         * the Step's retry policy. Servers older than server-zd 0.1.21 ignore the value.
         *
         * <pre>{@code
         * client.updateFlowConfig("order-42", FlowConfig.newBuilder()
         *         .historyWindowEvents(50_000)
         *         .build());
         * }</pre>
         *
         * @param value the number of newest history events to keep; must be positive
         * @return this builder
         * @since 1.38.0
         */
        public Builder historyWindowEvents(final int value) {
            historyWindowEvents = value;
            return this;
        }

        /**
         * Controls deletion of object storage objects for expired history segments.
         *
         * {@code true} deletes each expired segment's object about one hour after expiry. {@code false} keeps
         * objects until the run is deleted after retention. This only matters when history
         * offload is enabled. Omission uses {@code history.deleteExpiredObjects} on the server: true
         * for dev/local and false for serve. The run records that default at start.
         *
         * Set it through {@link StartFlowOptions.Builder#configOverride(FlowConfig)},
         * update it with {@link Client#updateFlowConfig(String, FlowConfig)}, or override it for a SubFlow. Children otherwise inherit the parent's value.
         * An omitted update preserves the current value; it cannot be cleared to the server
         * default. Enabling deletion includes already expired objects. Disabling it stops
         * deletions that have not started; requests already sent may complete, and deleted
         * objects never return. History reads and Time Travel exclude expired history even
         * when its objects are kept.
         *
         * <pre>{@code
         * client.updateFlowConfig("order-42", FlowConfig.newBuilder()
         *         .deleteExpiredHistoryObjects(false).build());
         * }</pre>
         *
         * @param value whether to delete expired history objects
         * @return this builder
         * @since 1.39.0
         */
        public Builder deleteExpiredHistoryObjects(final boolean value) {
            deleteExpiredHistoryObjects = value;
            return this;
        }

        /**
         * Builds immutable Flow configuration from the current values.
         *
         * @return the configured Flow settings
         */
        public FlowConfig build() {
            return new FlowConfig(this);
        }
    }
}
