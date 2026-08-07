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

public enum IdReusePolicy {
    DEFAULT,
    ALLOW_IF_PREVIOUS_FAILED,
    ALLOW_IF_NOT_RUNNING,
    DISALLOW,
    TERMINATE_IF_RUNNING
}
