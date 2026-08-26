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

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/**
 * Configures server behavior for one Flow execution.
 *
 * <p>Use this value as a start-time override, in an update request, or to select a worker target.
 * Unset properties remain under server control. Continue-as-new thresholds bound execution history
 * and page size; consult the server deployment configuration before overriding them.
 *
 * <pre>{@code
 * FlowConfig config = FlowConfig.newBuilder()
 *         .activeStepSearchMode(ActiveStepSearchMode.WITH_WAIT_FOR)
 *         .stepDurability(StepDurability.SYNC)
 *         .workerTarget(new WorkerTarget("orders:8803", false))
 *         .build();
 * }</pre>
 */
public final class FlowConfig {
    private final ActiveStepSearchMode activeStepSearchMode;
    private final Integer continueAsNewThreshold;
    private final Integer continueAsNewPageSizeBytes;
    private final StepDurability stepDurability;
    private final WorkerTarget workerTarget;
    private final List<String> attributeStoreNames;

    private FlowConfig(final Builder builder) {
        this.activeStepSearchMode = builder.activeStepSearchMode;
        this.continueAsNewThreshold = builder.continueAsNewThreshold;
        this.continueAsNewPageSizeBytes = builder.continueAsNewPageSizeBytes;
        this.stepDurability = builder.stepDurability;
        this.workerTarget = builder.workerTarget;
        this.attributeStoreNames = builder.attributeStoreNames;
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

    WorkerTarget getWorkerTarget() {
        return workerTarget;
    }

    List<String> getAttributeStoreNames() {
        return attributeStoreNames;
    }

    /** Builds immutable {@link FlowConfig} values. */
    public static final class Builder {
        private ActiveStepSearchMode activeStepSearchMode;
        private Integer continueAsNewThreshold;
        private Integer continueAsNewPageSizeBytes;
        private StepDurability stepDurability;
        private WorkerTarget workerTarget;
        private List<String> attributeStoreNames;

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
         * <p>The server default is {@link StepDurability#ASYNC}, which lowers latency and increases
         * server throughput by allowing result persistence to be batched. A method-level setting in
         * {@link StepOptions} takes precedence. This Flow-wide setting does not override Dex's safer
         * {@link StepDurability#SYNC} choice when a wait-for or execute failure policy proceeds.
         * Applications may override that choice only in the relevant Step options after considering
         * the replay tradeoff described by {@link StepDurability}.
         *
         * @param value the durability mode, or {@code null} for the server default
         * @return this builder
         */
        public Builder stepDurability(final StepDurability value) {
            stepDurability = value;
            return this;
        }

        /**
         * Routes this Flow to a specific worker target.
         *
         * @param value the worker target, or {@code null} for default routing
         * @return this builder
         */
        public Builder workerTarget(final WorkerTarget value) {
            workerTarget = value;
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
         * Builds immutable Flow configuration from the current values.
         *
         * @return the configured Flow settings
         */
        public FlowConfig build() {
            return new FlowConfig(this);
        }
    }
}
