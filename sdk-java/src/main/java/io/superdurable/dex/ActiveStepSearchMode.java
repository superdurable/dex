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
 * Controls which active Step types Dex records in the {@code ActiveStepTypes} search index.
 *
 * <p>Before renaming or removing a Step implementation, applications can search
 * {@code ActiveStepTypes} to find running Flows that still use that Step type and preserve
 * backward compatibility. Broader indexing provides more complete visibility but performs more
 * search-index updates. Narrower indexing reduces those updates but may omit active Steps from
 * search results.
 *
 * <pre>{@code
 * FlowConfig config = FlowConfig.newBuilder()
 *         .activeStepSearchMode(ActiveStepSearchMode.ALL)
 *         .build();
 * client.updateFlowConfig("order-123", config);
 *
 * SearchFlowsPage active = client.searchFlows(
 *         "ActiveStepTypes = 'ChargeOrder'",
 *         100);
 * }</pre>
 */
public enum ActiveStepSearchMode {
    /** Uses the server default, which indexes active Steps whose {@code waitFor} method is used. */
    DEFAULT,

    /** Indexes every active Step type, including Steps that execute without {@code waitFor}. */
    ALL,

    /** Indexes an active Step type only when its {@link Step#waitFor(Context, Object)} method runs. */
    WITH_WAIT_FOR,

    /** Does not index active Step types, so active-Step searches cannot find this Flow. */
    DISABLED
}
