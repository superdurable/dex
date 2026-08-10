/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex.exceptions;

public enum ErrorSubStatus {
    UNCATEGORIZED,
    FLOW_ALREADY_STARTED,
    FLOW_NOT_EXISTS,
    WORKER_API_ERROR,
    LONG_POLL_TIMEOUT
}
