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

public interface Step<I> {
    Class<I> getInputType();

    StepDecision execute(Context context, I input);

    default Wait waitFor(final Context context, final I input) {
        throw new IllegalStateException("framework must skip the default waitFor");
    }

    default String getStepType() {
        return getClass().getSimpleName();
    }

    default StepOptions getStepOptions() {
        return null;
    }
}
