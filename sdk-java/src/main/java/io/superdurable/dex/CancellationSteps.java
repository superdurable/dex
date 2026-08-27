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

    static List<Class<? extends Step<?>>> add(
            final List<Class<? extends Step<?>>> existing,
            final Class<? extends Step<?>>[] additions) {
        final List<Class<? extends Step<?>>> combined =
                new ArrayList<Class<? extends Step<?>>>(existing);
        if (additions == null) {
            combined.add(null);
            return combined;
        }
        for (Class<? extends Step<?>> addition : additions) {
            if (!contains(combined, addition)) {
                combined.add(addition);
            }
        }
        return combined;
    }

    static void remove(
            final List<Class<? extends Step<?>>> target,
            final List<Class<? extends Step<?>>> removals) {
        for (int index = target.size() - 1; index >= 0; index--) {
            if (contains(removals, target.get(index))) {
                target.remove(index);
            }
        }
    }

    static List<Class<? extends Step<?>>> immutable(
            final List<Class<? extends Step<?>>> steps) {
        return Collections.unmodifiableList(
                new ArrayList<Class<? extends Step<?>>>(steps));
    }

    private static boolean contains(
            final List<Class<? extends Step<?>>> steps,
            final Class<? extends Step<?>> candidate) {
        for (Class<? extends Step<?>> stepClass : steps) {
            if (stepClass == candidate) {
                return true;
            }
        }
        return false;
    }
}
