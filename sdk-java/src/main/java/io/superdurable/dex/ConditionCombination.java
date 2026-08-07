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

public final class ConditionCombination {
    private final List<Condition> conditions;

    private ConditionCombination(final List<Condition> conditions) {
        this.conditions = Collections.unmodifiableList(conditions);
    }

    public static ConditionCombination of(final Condition... conditions) {
        return new ConditionCombination(Arrays.asList(conditions.clone()));
    }

    List<Condition> getConditions() {
        return conditions;
    }
}
