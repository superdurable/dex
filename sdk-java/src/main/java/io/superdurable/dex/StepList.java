/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

/**
 * Declares a Flow's ordered collection of registered Steps.
 *
 * <p>The type parameter binds the Flow input to its start Step input. Use {@link #startStep} for a
 * normally started Flow, {@link #withoutStartStep} for an RPC-started Flow, or {@link #empty} for a
 * Flow with no Steps. Add remaining Steps fluently with {@link #otherSteps}.
 *
 * <pre>{@code
 * public StepList<OrderInput> getSteps() {
 *     return StepList.startStep(validate).otherSteps(charge, notifyCustomer);
 * }
 * }</pre>
 *
 * @param <StartInput> the Flow's start input type
 */
public final class StepList<StartInput> {
    private final List<StepDef> definitions;

    private StepList(final List<StepDef> definitions) {
        this.definitions = Collections.unmodifiableList(definitions);
    }

    /**
     * Creates an empty Step list for a Flow with no Steps.
     *
     * @param <StartInput> the declared Flow start input type
     * @return an empty immutable Step list
     */
    public static <StartInput> StepList<StartInput> empty() {
        return new StepList<StartInput>(Collections.<StepDef>emptyList());
    }

    /**
     * Creates a Step list with a typed start Step.
     *
     * @param startStep the nonnull start Step whose input matches the Flow input
     * @param <StartInput> the Flow and start-Step input type
     * @return an immutable Step list containing the start Step
     * @throws NullPointerException if {@code startStep} is {@code null}
     */
    public static <StartInput> StepList<StartInput> startStep(
            final Step<StartInput> startStep) {
        final List<StepDef> definitions = new ArrayList<StepDef>();
        definitions.add(StepDef.startStep(Objects.requireNonNull(startStep, "startStep")));
        return new StepList<StartInput>(definitions);
    }

    /**
     * Creates a Step list with no start Step and one or more RPC-targetable Steps.
     *
     * @param steps the nonnull non-start Steps
     * @param <StartInput> the declared Flow input type
     * @return an immutable Step list without a start Step
     * @throws NullPointerException if the array or any Step is {@code null}
     */
    public static <StartInput> StepList<StartInput> withoutStartStep(
            final Step<?>... steps) {
        return new StepList<StartInput>(nonStartDefinitions(steps));
    }

    /**
     * Returns a new list with additional non-start Steps.
     *
     * @param steps the nonnull Steps to append
     * @return a new immutable Step list; the original list is unchanged
     * @throws NullPointerException if the array or any Step is {@code null}
     */
    public StepList<StartInput> otherSteps(final Step<?>... steps) {
        final List<StepDef> combined = new ArrayList<StepDef>(definitions);
        combined.addAll(nonStartDefinitions(steps));
        return new StepList<StartInput>(combined);
    }

    List<StepDef> getDefinitions() {
        return definitions;
    }

    private static List<StepDef> nonStartDefinitions(final Step<?>... steps) {
        Objects.requireNonNull(steps, "steps");
        final List<StepDef> definitions = new ArrayList<StepDef>(steps.length);
        for (Step<?> step : steps) {
            definitions.add(StepDef.nonStartStep(Objects.requireNonNull(step, "step")));
        }
        return definitions;
    }
}
