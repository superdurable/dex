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
 * Controls when a Step method's effects are durably committed.
 *
 * <p>Configure wait and execute methods independently with {@link StepOptions.Builder}. Synchronous
 * durability waits for persistence before acknowledging method completion, while asynchronous
 * durability may acknowledge earlier for lower latency.
 */
public enum StepDurability {
    /** Uses the Dex server's default durability mode. */
    DEFAULT,

    /** Persists the method result synchronously before acknowledging completion. */
    SYNC,

    /** Allows the method result to be persisted asynchronously. */
    ASYNC
}
