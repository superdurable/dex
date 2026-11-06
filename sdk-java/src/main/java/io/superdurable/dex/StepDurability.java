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
 * Selects when Dex durably records a Step method result relative to subsequent Flow work.
 *
 * <p>{@link #ASYNC} lets Dex continue the Flow without first waiting for the completed method result
 * to be persisted. This lowers latency between Steps and makes single Flows faster. Sustained
 * throughput is about the same as SYNC. The tradeoff is a small failure window: if the result
 * has not yet been persisted when the Server fails, Dex may not see
 * that completion during recovery and may invoke the already-completed Step method again. Step
 * methods must therefore be idempotent regardless of the selected durability.
 *
 * <p>{@link #SYNC} waits for the method result to be durably recorded before Dex advances based on
 * that result. It adds persistence latency and prevents the same degree of server-side batching, but
 * it closes the asynchronous result-loss window. It does not provide exactly-once execution, so Step
 * methods must still be safe to retry.
 *
 * <p>The server default for Step methods is {@link #SYNC}. Use
 * {@link FlowConfig.Builder#stepDurability} to override the default for a Flow. Use
 * {@link StepOptions.Builder#waitForDurability} or
 * {@link StepOptions.Builder#executeDurability} to override one method on a specific Step; these
 * method-level choices take precedence over the Flow configuration.
 *
 * <p>With Server protocol 4 (server-zd v0.1.17, required by SDK 1.35.0): SYNC also persists
 * the attempt start before handing the task to a Worker. ASYNC can hand it out before the start
 * is persisted. A server failure can roll back that ASYNC start and rerun the same attempt number
 * without consuming a retry, possibly with different input. The two executions can overlap. An
 * unpersisted ASYNC result can also replay later work that already started from it. Stream writes
 * can repeat under the same source. A Worker disconnection after hand-out fails the attempt and
 * consumes a retry in both modes.
 *
 * <p>Wait-for and execute failure policies do not change this precedence. A Flow-wide asynchronous
 * default therefore applies to methods with failure policies unless the Step method overrides it.
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
     * Uses the applicable Flow configuration or the synchronous server default.
     *
     * <p>A method-level DEFAULT defers to {@link FlowConfig}. Without a Flow override, it resolves to
     * {@link #SYNC}. Failure policies do not change the result.
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
     * <p>This lowers latency and makes single Flows faster, with about the same sustained throughput, while
     * accepting that an extreme failure may lose an unpersisted result and cause the Step method to
     * execute again.
     */
    ASYNC
}
