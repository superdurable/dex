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

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

final class CancellationSteps {
    private CancellationSteps() {
    }

    static List<Step<?>> add(
            final List<Step<?>> existing,
            final Step<?>[] additions) {
        final List<Step<?>> combined = new ArrayList<Step<?>>(existing);
        if (additions == null) {
            combined.add(null);
            return combined;
        }
        for (Step<?> addition : additions) {
            if (!contains(combined, addition)) {
                combined.add(addition);
            }
        }
        return combined;
    }

    static void remove(
            final List<Step<?>> target,
            final List<Step<?>> removals) {
        for (int index = target.size() - 1; index >= 0; index--) {
            if (contains(removals, target.get(index))) {
                target.remove(index);
            }
        }
    }

    static List<Step<?>> immutable(final List<Step<?>> steps) {
        return Collections.unmodifiableList(new ArrayList<Step<?>>(steps));
    }

    private static boolean contains(
            final List<Step<?>> steps,
            final Step<?> candidate) {
        for (Step<?> step : steps) {
            if (step == candidate) {
                return true;
            }
        }
        return false;
    }
}
