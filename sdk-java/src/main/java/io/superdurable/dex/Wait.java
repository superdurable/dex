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

    public static Wait skipImmediately() {
        return ofConditions(Kind.SKIP_IMMEDIATELY);
    }

    public static Wait allOf(final Condition... conditions) {
        return ofConditions(Kind.ALL_OF, conditions);
    }

    public static Wait anyOf(final Condition... conditions) {
        return ofConditions(Kind.ANY_OF, conditions);
    }

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
