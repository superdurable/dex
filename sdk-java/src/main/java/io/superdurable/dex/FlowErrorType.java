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
 * Classifies why a Flow reached a terminal status other than {@link FlowStatus#COMPLETED}.
 *
 * <p>{@link FlowResult#getErrorType()} exposes this value after Dex records a Flow failure.
 */
public enum FlowErrorType {
    /** A Step returned a decision that failed the Flow. */
    STEP_DECISION_FAILED,

    /** A client API operation caused the Flow to fail. */
    CLIENT_API_FAILED,

    /** A worker API invocation caused the Flow to fail. */
    WORKER_API_FAILED,

    /** Dex rejected invalid user Flow code or an invalid definition. */
    INVALID_USER_FLOW_CODE,

    /** Dex encountered an internal failure. */
    INTERNAL,

    /** A Dex soft Flow timeout expired under {@link FlowTimeoutPolicy#FAIL}. */
    FLOW_TIMEOUT,

    /**
     * The parent Flow stopped waiting for this SubFlow child.
     *
     * <p>A Time Travel restored a parent Step that waited on a SubFlow condition, and its SubFlow
     * reset policy chose to fail that condition. Dex terminates the child run if it still runs.
     * The condition then completes with a {@link FlowStatus#FAILED} result that carries this error
     * type. Only a SubFlow result read in Execute carries it; the child run itself never closes
     * with it.
     *
     * <p>Handle it like any other failed child: retry, compensate, or fail the parent. The child's
     * output is not available.
     *
     * <pre>{@code
     * FlowResult result = SubFlow.getConditionResults(context);
     * if (result.getErrorType() == FlowErrorType.SUB_FLOW_ABANDONED) {
     *     throw new IllegalStateException("child abandoned by a Time Travel");
     * }
     * }</pre>
     *
     * @since 1.45.2
     */
    SUB_FLOW_ABANDONED
}
