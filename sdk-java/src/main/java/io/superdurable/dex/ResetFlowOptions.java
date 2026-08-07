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

import java.time.Instant;

public final class ResetFlowOptions {
    private final ResetType type;
    private final Long historyEventId;
    private final Instant historyEventTime;
    private final String stepType;
    private final String stepExecutionId;
    private final String reason;
    private final boolean skipChannelMessagesReapply;
    private final boolean skipLockingRpcReapply;

    private ResetFlowOptions(final Builder builder) {
        this.type = builder.type;
        this.historyEventId = builder.historyEventId;
        this.historyEventTime = builder.historyEventTime;
        this.stepType = builder.stepType;
        this.stepExecutionId = builder.stepExecutionId;
        this.reason = builder.reason;
        this.skipChannelMessagesReapply = builder.skipChannelMessagesReapply;
        this.skipLockingRpcReapply = builder.skipLockingRpcReapply;
    }

    public static Builder newBuilder(final ResetType type) {
        return new Builder(type);
    }

    ResetType getType() {
        return type;
    }

    Long getHistoryEventId() {
        return historyEventId;
    }

    Instant getHistoryEventTime() {
        return historyEventTime;
    }

    String getStepType() {
        return stepType;
    }

    String getStepExecutionId() {
        return stepExecutionId;
    }

    String getReason() {
        return reason;
    }

    boolean isSkipChannelMessagesReapply() {
        return skipChannelMessagesReapply;
    }

    boolean isSkipLockingRpcReapply() {
        return skipLockingRpcReapply;
    }

    public static final class Builder {
        private final ResetType type;
        private Long historyEventId;
        private Instant historyEventTime;
        private String stepType;
        private String stepExecutionId;
        private String reason;
        private boolean skipChannelMessagesReapply;
        private boolean skipLockingRpcReapply;

        private Builder(final ResetType type) {
            this.type = type;
        }

        public Builder historyEventId(final long value) {
            historyEventId = value;
            return this;
        }

        public Builder historyEventTime(final Instant value) {
            historyEventTime = value;
            return this;
        }

        public Builder stepType(final String value) {
            stepType = value;
            return this;
        }

        public Builder stepExecutionId(final String value) {
            stepExecutionId = value;
            return this;
        }

        public Builder reason(final String value) {
            reason = value;
            return this;
        }

        public Builder skipChannelMessagesReapply(final boolean value) {
            skipChannelMessagesReapply = value;
            return this;
        }

        public Builder skipLockingRpcReapply(final boolean value) {
            skipLockingRpcReapply = value;
            return this;
        }

        public ResetFlowOptions build() {
            return new ResetFlowOptions(this);
        }
    }
}
