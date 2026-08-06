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
