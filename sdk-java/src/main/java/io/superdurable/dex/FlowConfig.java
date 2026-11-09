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
    private final Boolean offloadHistory;

    private FlowConfig(final Builder builder) {
        this.activeStepSearchMode = builder.activeStepSearchMode;
        this.continueAsNewThreshold = builder.continueAsNewThreshold;
        this.continueAsNewPageSizeBytes = builder.continueAsNewPageSizeBytes;
        this.stepDurability = builder.stepDurability;
        this.attributeStoreNames = builder.attributeStoreNames;
        this.historyWindowEvents = builder.historyWindowEvents;
        this.offloadHistory = builder.offloadHistory;
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

    Integer getHistoryWindowEvents() {
        return historyWindowEvents;
    }

    Boolean getOffloadHistory() {
        return offloadHistory;
    }

    /** Builds immutable {@link FlowConfig} values. */
    public static final class Builder {
        private ActiveStepSearchMode activeStepSearchMode;
        private Integer continueAsNewThreshold;
        private Integer continueAsNewPageSizeBytes;
        private StepDurability stepDurability;
        private List<String> attributeStoreNames;
        private Integer historyWindowEvents;
        private Boolean offloadHistory;

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
         * Has no effect. The SDK does not send this value to the server.
         *
         * <p>server-zd 0.1.23 and later ignore the field for runs that start with them. Such a
         * run, when it offloads its history, always deletes the objects of expired history
         * segments about one hour after the history window passes them.
         *
         * <p>A run that started under server-zd 0.1.22 still follows the value it recorded then.
         * When that value is false, the run's expired history objects stay in object storage until
         * its retention ends, and a run that never closes keeps them. To delete them sooner, set
         * the field to true for that run with UpdateFlowConfig through an SDK older than 1.40.0 or
         * a direct gRPC request. See
         * <a href="https://docs.superdurable.io/reference/server/configuration#history-offload">History offload</a>.
         *
         * <p>The method remains so existing code still compiles.
         *
         * @param value ignored
         * @return this builder
         * @since 1.39.0
         * @deprecated Deprecated in 1.40.0. Has no effect, because the SDK no longer sends it and
         *     runs that start with server-zd 0.1.23 or later ignore it. Remove the call.
         */
        @Deprecated
        public Builder deleteExpiredHistoryObjects(final boolean value) {
            return this;
        }

        /**
         * Sets whether Dex offloads the run's history to object storage.
         *
         * <p>Omitting this call means {@code true}. Leave it unset for most runs.
         *
         * <p>An offloaded run uploads its history to object storage in segment objects. Once a
         * segment is uploaded and a grace period has passed, Dex deletes its history records and
         * superseded values from the local disk. History reads then fetch them from object storage.
         * When the history window passes a whole segment, Dex deletes the segment's object about one
         * hour later.
         *
         * <p>A run that does not offload keeps its history on the local disk. It never writes a
         * history object. The history window still applies: Dex prunes the oldest events and
         * deletes the superseded values that the window passed.
         *
         * <p>A long-running run that offloads keeps only its newest history and current state on the
         * local disk. Pass {@code false} for many short runs that close soon after they start. Each
         * offloaded run writes at least one history object when it closes. It deletes that object
         * when its retention ends. Short runs at a high rate therefore add object storage requests,
         * while their small history costs little local disk.
         * A run that does not offload keeps every superseded value its history window
         * covers on the local disk, with no byte limit; for a run that rewrites large
         * values, keep offload or set a smaller history window.
         *
         * <p>Set it only at start, through {@link StartFlowOptions.Builder#configOverride(FlowConfig)},
         * or for a SubFlow through its {@link SubFlowOptions} override. A SubFlow inherits the value
         * from its parent unless its own override sets it. The run keeps the value for its whole
         * life. {@link Client#updateFlowConfig(String, FlowConfig)} with this value set throws
         * {@link io.superdurable.dex.exceptions.DexServiceException} whose {@code getCode()} is
         * {@code INVALID_ARGUMENT}. Runs that started before server-zd 0.1.23 keep the history
         * offload behavior that their server gave them. Servers older than server-zd 0.1.23 ignore
         * the value.
         *
         * <pre>{@code
         * client.startFlow(new NotificationFlow(), "notification-42", input,
         *         StartFlowOptions.newBuilder()
         *                 .configOverride(FlowConfig.newBuilder()
         *                         .offloadHistory(false)
         *                         .build())
         *                 .build());
         * }</pre>
         *
         * @param value {@code true} to offload the run's history to object storage, or
         *     {@code false} to keep it on the local disk
         * @return this builder
         * @since 1.40.0
         */
        public Builder offloadHistory(final boolean value) {
            offloadHistory = value;
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
