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

/** Controls how Dex responds when a positive soft Flow timeout expires. */
public enum FlowTimeoutPolicy {
    /** Uses {@link Flow#handleTimeout} when overridden, and {@link #FAIL} otherwise. */
    DEFAULT,

    /** Fails the Flow with {@link FlowErrorType#FLOW_TIMEOUT} and permits Flow retries. */
    FAIL,

    /** Cancels the Flow without retrying it. */
    CANCEL,

    /** Starts one logical {@link Flow#handleTimeout} execution after the durable timer completes or is skipped. */
    HANDLER
}
