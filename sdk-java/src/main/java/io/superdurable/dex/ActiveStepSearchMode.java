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

/**
 * Controls which active Steps Dex searches when routing work.
 *
 * <p>Use this setting through {@link FlowConfig.Builder#activeStepSearchMode} when a Flow needs
 * an explicit search policy. Most applications should keep {@link #DEFAULT} so the server can
 * apply its configured policy.
 *
 * <pre>{@code
 * FlowConfig config = FlowConfig.newBuilder()
 *         .activeStepSearchMode(ActiveStepSearchMode.WITH_WAIT_FOR)
 *         .build();
 * }</pre>
 */
public enum ActiveStepSearchMode {
    /** Uses the Dex server's default active-Step search policy. */
    DEFAULT,

    /** Searches every active Step when routing work. */
    ALL,

    /** Searches only active Steps that define a {@link Step#waitFor(Context, Object)} method. */
    WITH_WAIT_FOR,

    /** Disables active-Step searching. */
    DISABLED
}
