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

import java.util.Collections;
import java.util.List;

public interface Flow<I> {
    default String getFlowType() {
        return getClass().getSimpleName();
    }

    default List<StepDef> getSteps() {
        return Collections.emptyList();
    }

    default PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }
}
