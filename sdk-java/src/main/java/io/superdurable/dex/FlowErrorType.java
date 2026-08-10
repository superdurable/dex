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

import io.superdurable.dex.exceptions.FlowUncompletedException;

/**
 * Classifies why a Flow reached a terminal status other than {@link FlowStatus#COMPLETED}.
 *
 * <p>{@link FlowUncompletedException#getErrorType()} exposes this value after
 * {@link Client#waitForFlow(String)} observes that status. It describes the Flow failure recorded
 * by Dex rather than the Java exception type thrown by the client.
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
    INTERNAL
}
