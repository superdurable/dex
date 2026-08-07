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

public final class TimerId {
    private final String conditionId;
    private final Integer index;

    private TimerId(final String conditionId, final Integer index) {
        this.conditionId = conditionId;
        this.index = index;
    }

    public static TimerId byConditionId(final String conditionId) {
        return new TimerId(Attribute.requireName(conditionId), null);
    }

    public static TimerId byConditionIndex(final int index) {
        return new TimerId(null, index);
    }

    String getConditionId() {
        return conditionId;
    }

    Integer getIndex() {
        return index;
    }
}
