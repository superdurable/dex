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

import java.util.Arrays;
import java.util.Collections;
import java.util.List;

/**
 * Groups conditions that must all be satisfied as one alternative in a combined wait.
 *
 * <p>Pass one or more combinations to {@link Wait#anyCombinationOf}. Dex resumes the Step when all
 * conditions in any one combination are satisfied.
 *
 * <pre>{@code
 * return Wait.anyCombinationOf(
 *         ConditionCombination.of(approval.forOne(), Timer.byDuration(shortDelay)),
 *         ConditionCombination.of(manualOverride.forOne()));
 * }</pre>
 */
public final class ConditionCombination {
    private final List<Condition> conditions;

    private ConditionCombination(final List<Condition> conditions) {
        this.conditions = Collections.unmodifiableList(conditions);
    }

    /**
     * Creates a combination whose conditions must all be satisfied.
     *
     * @param conditions the conditions in this alternative
     * @return an immutable condition combination
     */
    public static ConditionCombination of(final Condition... conditions) {
        return new ConditionCombination(Arrays.asList(conditions.clone()));
    }

    List<Condition> getConditions() {
        return conditions;
    }
}
