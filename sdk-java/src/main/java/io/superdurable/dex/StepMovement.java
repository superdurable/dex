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
 * Describes one transition to a typed Step and its input.
 *
 * <p>Use movements when a decision or RPC starts one or more next Steps. Options supplied to a
 * movement override the target Step's default options for that execution only.
 *
 * <pre>{@code
 * return StepDecision.goToMany(
 *         StepMovement.of(ReserveInventory.class, order),
 *         StepMovement.of(AuthorizePayment.class, order, paymentOptions));
 * }</pre>
 *
 * @param <I> the target Step's input type
 */
public final class StepMovement<I> {
    private final Class<? extends Step<I>> stepClass;
    private final I input;
    private final StepOptions options;

    private StepMovement(
            final Class<? extends Step<I>> stepClass,
            final I input,
            final StepOptions options) {
        this.stepClass = stepClass;
        this.input = input;
        this.options = options;
    }

    /**
     * Creates a movement using the target Step's default options.
     *
     * @param stepClass the target Step class
     * @param input the typed input for that Step
     * @param <I> the target Step's input type
     * @return the typed Step movement
     */
    public static <I> StepMovement<I> of(
            final Class<? extends Step<I>> stepClass,
            final I input) {
        return new StepMovement<I>(stepClass, input, null);
    }

    /**
     * Creates a movement with per-execution Step options.
     *
     * @param stepClass the target Step class
     * @param input the typed input for that Step
     * @param options the per-execution options, or {@code null} to use Step defaults
     * @param <I> the target Step's input type
     * @return the typed Step movement
     */
    public static <I> StepMovement<I> of(
            final Class<? extends Step<I>> stepClass,
            final I input,
            final StepOptions options) {
        return new StepMovement<I>(stepClass, input, options);
    }

    Class<? extends Step<I>> getStepClass() {
        return stepClass;
    }

    I getInput() {
        return input;
    }

    StepOptions getOptions() {
        return options;
    }
}
