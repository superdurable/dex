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
 * Selects when Dex durably records a Step method result relative to subsequent Flow work.
 *
 * <p>{@link #ASYNC} lets Dex continue the Flow without first waiting for the completed method result
 * to be persisted. This lowers the latency between Steps and lets the server batch persistence work,
 * which generally provides higher throughput. The tradeoff is a small failure window: if the result
 * has not yet been persisted when the underlying process or workflow engine fails, Dex may not see
 * that completion during recovery and may invoke the already-completed Step method again. Step
 * methods must therefore be idempotent regardless of the selected durability.
 *
 * <p>{@link #SYNC} waits for the method result to be durably recorded before Dex advances based on
 * that result. It adds persistence latency and prevents the same degree of server-side batching, but
 * it closes the asynchronous result-loss window. It does not provide exactly-once execution, so Step
 * methods must still be safe to retry.
 *
 * <p>The server default for ordinary Step methods is {@link #ASYNC}. Use
 * {@link FlowConfig.Builder#stepDurability} to change the default for a Flow. Use
 * {@link StepOptions.Builder#waitForDurability} or
 * {@link StepOptions.Builder#executeDurability} to override one method on a specific Step; these
 * method-level choices take precedence over the Flow configuration.
 *
 * <p>Failure policies use a safer server default. When
 * {@link StepOptions.Builder#waitForFailure} allows execution to proceed after a wait-for failure,
 * or {@link StepOptions.Builder#onExecuteFailureProceedTo} routes an execute failure to a recovery
 * Step, Dex defaults to {@link #SYNC} for the affected method. A Flow-wide durability setting does not
 * override that server choice. The application may still select {@link #ASYNC} explicitly in the
 * relevant {@link StepOptions} when its business semantics accept the replay risk.
 *
 * <p>With asynchronous persistence on a failure-policy path, an extreme failure after execution or
 * recovery has begun can cause Dex to resume from the earlier method and invoke it again. That replay
 * is often harmless for idempotent Steps, but it may violate business expectations after recovery
 * logic has already run. Requiring a method-level override makes this tradeoff an explicit application
 * decision rather than an accidental consequence of the Flow default.
 *
 * <pre>{@code
 * FlowConfig fastByDefault = FlowConfig.newBuilder()
 *         .stepDurability(StepDurability.ASYNC)
 *         .build();
 *
 * public StepOptions getStepOptions() {
 *     return StepOptions.newBuilder()
 *             .waitForFailure(WaitForFailurePolicy.PROCEED)
 *             .waitForDurability(StepDurability.SYNC)
 *             .executeDurability(StepDurability.SYNC)
 *             .onExecuteFailureProceedTo(recoveryStep)
 *             .build();
 * }
 * }</pre>
 */
public enum StepDurability {
    /**
     * Uses the applicable Flow configuration or the server's failure-policy safety choice.
     *
     * <p>For an ordinary Step method with no Flow-level override, this resolves to the server default,
     * which is {@link #ASYNC}. For a method using a proceed-on-failure policy, Dex instead defaults to
     * {@link #SYNC}; only an explicit method-level choice in {@link StepOptions} overrides that safety
     * choice.
     */
    DEFAULT,

    /**
     * Persists the method result before Dex advances based on it.
     *
     * <p>This adds persistence latency and reduces batching opportunities, but prevents a completed
     * result from being lost solely because Dex advanced before recording it. Retries can still occur,
     * so this mode does not remove the idempotency requirement.
     */
    SYNC,

    /**
     * Allows Dex to advance before the completed method result is durably recorded.
     *
     * <p>This is the server default. It lowers latency and improves server throughput through batched
     * persistence, while accepting that an extreme failure may lose an unpersisted result and cause
     * the Step method to execute again.
     */
    ASYNC
}
