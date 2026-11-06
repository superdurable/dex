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

/**
 * Describes how far a failed Step method progressed across all its attempts.
 *
 * <p>Use {@link RecoveryErrorInfo#getAttemptProgress()} during recovery to decide whether handler
 * side effects may need reconciliation. Only {@link #NOT_STARTED} rules out handler execution.
 * See that accessor for an example.
 *
 * <p>ASYNC failures always report NOT_COMPLETED because an unpersisted attempt start may have
 * been rolled back after application code ran. NOT_STARTED is available only for SYNC.
 * NOT_COMPLETED is conservative: hand-out followed by a disconnect or input decoding failure
 * can report it even if the handler never ran.
 *
 * @since 1.35.0
 */
public enum StepAttemptProgress {
    /**
     * The Server did not report progress; treat this as {@link #NOT_COMPLETED}.
     * This includes failures recorded before progress reporting was available.
     */
    UNSPECIFIED,
    /** The handler did not run in any attempt of this Step method call. */
    NOT_STARTED,
    /** Some attempt may have run, but none completed; side effects may have happened. */
    NOT_COMPLETED;

    static StepAttemptProgress fromProto(final io.superdurable.gen.StepAttemptProgress progress) {
        switch (progress) {
            case STEP_ATTEMPT_PROGRESS_NOT_STARTED:
                return NOT_STARTED;
            case STEP_ATTEMPT_PROGRESS_NOT_COMPLETED:
                return NOT_COMPLETED;
            default:
                return UNSPECIFIED;
        }
    }
}
