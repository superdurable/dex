/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Sustainable Use License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.StepDecision;

/** Writes a declared Attribute without a lock from its unchecked timeout handler. */
final class AttributeLockRequirementTimeoutWorkflow implements Flow<Void> {
    static final String EXPIRED = "expired";

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return AttributeLockRequirementStepWorkflow.lockRequirementSchema();
    }

    @Override
    public StepDecision handleTimeout(final Context context) {
        AttributeLockRequirementStepWorkflow.TOTAL.set(context, 5);
        return StepDecision.forceComplete(EXPIRED);
    }
}
