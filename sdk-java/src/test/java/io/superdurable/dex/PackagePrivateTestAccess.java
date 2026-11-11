/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import java.util.List;

/** Exposes package-private SDK state to tests outside the SDK package. */
public final class PackagePrivateTestAccess {
    private PackagePrivateTestAccess() {
    }

    public static List<PersistenceDefinition> channelsOf(final PersistenceSchema schema) {
        return schema.getChannels();
    }

    public static List<StepMovement<?>> movementsOf(final StepDecision decision) {
        return decision.getMovements();
    }

    public static List<StepMovement<?>> nextStepsOf(final RPCResult<?> result) {
        return result.getNextSteps();
    }

    public static <O> O outputOf(final RPCResult<O> result) {
        return result.getOutput();
    }
}
