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

import io.superdurable.dex.exceptions.InvalidStepResultException;

import java.util.Arrays;
import java.util.Collections;
import java.util.List;

/**
 * Describes when Dex may invoke a Step's execute method.
 *
 * <p>Return a {@code Wait} from {@link Step#waitFor}. The value is declarative and immutable: Dex
 * persists and evaluates its Timer, Channel, and SubFlow conditions without blocking the worker
 * thread that produced it. Use {@link #until} for a single condition, {@link #allOf} or
 * {@link #anyOf} for a flat group, and {@link #anyCombinationOf} for alternatives containing
 * multiple conditions.
 *
 * <pre>{@code
 * public Wait waitFor(Context context, Order input) {
 *     return Wait.anyOf(
 *             paymentReceived.forOne("payment"),
 *             Timer.byDuration(Duration.ofHours(1), "timeout"));
 * }
 * }</pre>
 */
public final class Wait {
    enum Kind {
        SKIP_IMMEDIATELY,
        ALL_OF,
        ANY_OF,
        ANY_COMBINATION_OF
    }

    private final Kind kind;
    private final List<Condition> conditions;
    private final List<ConditionCombination> combinations;

    private Wait(
            final Kind kind,
            final List<Condition> conditions,
            final List<ConditionCombination> combinations) {
        this.kind = kind;
        this.conditions = Collections.unmodifiableList(conditions);
        this.combinations = Collections.unmodifiableList(combinations);
    }

    /**
     * Skips condition evaluation and makes the Step immediately eligible to execute.
     *
     * @return an immediate wait definition
     */
    public static Wait skipImmediately() {
        return ofConditions(Kind.SKIP_IMMEDIATELY);
    }

    /**
     * Waits for one condition to be satisfied.
     *
     * @param condition the single Timer, Channel, or SubFlow condition
     * @return a wait equivalent to {@code allOf(condition)}
     */
    public static Wait until(final Condition condition) {
        return allOf(condition);
    }

    /**
     * Waits until every supplied condition is satisfied.
     *
     * @param conditions the conditions that must all be satisfied
     * @return an all-of wait definition
     */
    public static Wait allOf(final Condition... conditions) {
        return ofConditions(Kind.ALL_OF, conditions);
    }

    /**
     * Waits until any supplied condition is satisfied.
     *
     * <p>Channel consumption is not greedy across alternatives. Dex consumes messages only from
     * the selected Channel condition; other ready Channel conditions consume nothing.
     *
     * @param conditions the alternative conditions
     * @return an any-of wait definition
     */
    public static Wait anyOf(final Condition... conditions) {
        return ofConditions(Kind.ANY_OF, conditions);
    }

    /**
     * Waits until every condition in any one supplied combination is satisfied.
     *
     * <p>Channel consumption is not greedy across combinations. Dex consumes messages only from
     * Channel conditions in the selected combination. Conditions belonging only to other ready
     * combinations consume nothing.
     *
     * <p>Every Condition must have a non-empty user-provided ID. Reusing the same Condition object
     * across combinations is supported; distinct Conditions must not share an ID.
     *
     * @param combinations alternative all-of condition groups
     * @return an any-combination wait definition
     * @throws InvalidStepResultException when a Condition ID is missing, empty, or duplicated
     */
    public static Wait anyCombinationOf(final ConditionCombination... combinations) {
        return new Wait(
                Kind.ANY_COMBINATION_OF,
                Collections.<Condition>emptyList(),
                Arrays.asList(combinations.clone()));
    }

    private static Wait ofConditions(final Kind kind, final Condition... conditions) {
        return new Wait(
                kind,
                Arrays.asList(conditions.clone()),
                Collections.<ConditionCombination>emptyList());
    }

    Kind getKind() {
        return kind;
    }

    List<Condition> getConditions() {
        return conditions;
    }

    List<ConditionCombination> getCombinations() {
        return combinations;
    }
}
