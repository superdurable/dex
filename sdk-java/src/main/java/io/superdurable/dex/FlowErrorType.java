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

public enum FlowErrorType {
    STEP_DECISION_FAILED,
    CLIENT_API_FAILED,
    WORKER_API_FAILED,
    INVALID_USER_FLOW_CODE,
    INTERNAL
}
