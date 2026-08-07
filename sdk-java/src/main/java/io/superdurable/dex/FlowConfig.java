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

public final class FlowConfig {
    private final ActiveStepSearchMode activeStepSearchMode;
    private final Integer continueAsNewThreshold;
    private final Integer continueAsNewPageSizeBytes;
    private final StepDurability stepDurability;
    private final WorkerTarget workerTarget;

    private FlowConfig(final Builder builder) {
        this.activeStepSearchMode = builder.activeStepSearchMode;
        this.continueAsNewThreshold = builder.continueAsNewThreshold;
        this.continueAsNewPageSizeBytes = builder.continueAsNewPageSizeBytes;
        this.stepDurability = builder.stepDurability;
        this.workerTarget = builder.workerTarget;
    }

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

    public static final class Builder {
        private ActiveStepSearchMode activeStepSearchMode;
        private Integer continueAsNewThreshold;
        private Integer continueAsNewPageSizeBytes;
        private StepDurability stepDurability;
        private WorkerTarget workerTarget;

        private Builder() {
        }

        public Builder activeStepSearchMode(final ActiveStepSearchMode value) {
            activeStepSearchMode = value;
            return this;
        }

        public Builder continueAsNewThreshold(final int value) {
            continueAsNewThreshold = value;
            return this;
        }

        public Builder continueAsNewPageSizeBytes(final int value) {
            continueAsNewPageSizeBytes = value;
            return this;
        }

        public Builder stepDurability(final StepDurability value) {
            stepDurability = value;
            return this;
        }

        public Builder workerTarget(final WorkerTarget value) {
            workerTarget = value;
            return this;
        }

        public FlowConfig build() {
            return new FlowConfig(this);
        }
    }
}
