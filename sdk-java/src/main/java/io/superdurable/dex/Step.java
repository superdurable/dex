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
 * Implements one durable unit of Flow logic.
 *
 * <p>Dex invokes {@link #waitFor} to declare durable conditions and invokes {@link #execute} after
 * those conditions are satisfied. Methods are synchronous Java calls executed by the worker's
 * bounded executor; do not return futures. A Step instance is shared across invocations, so keep
 * per-execution state in {@link Context}, Attributes, or method-local variables rather than mutable
 * instance fields.
 *
 * <pre>{@code
 * final class ChargeOrder implements Step<OrderInput> {
 *     @Override
 *     public Class<OrderInput> getInputType() {
 *         return OrderInput.class;
 *     }
 *
 *     @Override
 *     public Wait waitFor(Context context, OrderInput input) {
 *         return Wait.until(payment.forOne());
 *     }
 *
 *     @Override
 *     public StepDecision execute(Context context, OrderInput input) {
 *         return StepDecision.gracefulComplete(input.orderId);
 *     }
 * }
 * }</pre>
 *
 * @param <I> the concrete Java input type accepted by this Step
 */
public interface Step<I> {
    /**
     * Returns the concrete class used to decode this Step's input.
     *
     * <p>The class must agree with {@code I}. A parameterized input such as {@code List<Order>}
     * cannot be returned because {@code List.class} does not retain its element type. Wrap the list
     * in a concrete input class and return that wrapper's class, or use {@code Order[].class} when an
     * array fits the input model.
     *
     * @return the concrete, nonnull Step input class
     */
    Class<I> getInputType();

    /**
     * Executes the Step's application logic and returns its durable decision.
     *
     * @param context the invocation-scoped Flow context
     * @param input the decoded Step input, which may be {@code null} for {@link Void} Steps
     * @return the nonnull decision describing the next durable action
     */
    StepDecision execute(Context context, I input);

    /**
     * Declares the conditions that must be satisfied before execution.
     *
     * <p>When this method is not overridden, the registry marks the Step to skip wait-for invocation
     * and execute immediately. An override may also return {@link Wait#skipImmediately()} explicitly.
     *
     * @param context the invocation-scoped Flow context
     * @param input the decoded Step input
     * @return the nonnull durable wait definition
     */
    default Wait waitFor(final Context context, final I input) {
        throw new IllegalStateException("framework must skip the default waitFor");
    }

    /**
     * Returns the Step type used to register this Step.
     *
     * <p>The default is {@code getClass().getSimpleName()}. Use an explicit named class or override
     * this method so refactoring does not accidentally change the Step type stored by Dex.
     *
     * @return the nonblank Step type
     */
    default String getStepType() {
        return getClass().getSimpleName();
    }

    /**
     * Returns retry, timeout, durability, locking, and failure behavior for this Step.
     *
     * @return Step-specific options, or {@code null} to use all defaults
     */
    default StepOptions getStepOptions() {
        return null;
    }
}
