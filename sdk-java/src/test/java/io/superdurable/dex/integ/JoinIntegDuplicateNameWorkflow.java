/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.ChannelMap;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Join;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;

/** Registers a Join and a ChannelMap with the same name, which registration must reject. */
class JoinIntegDuplicateNameWorkflow implements Flow<String> {
    static final String DUPLICATED_NAME = "approvalResult";

    final Join<String> approvalResults = Join.define(DUPLICATED_NAME, String.class);
    final ChannelMap<String> approvalCommands = ChannelMap.define(DUPLICATED_NAME, String.class);
    private final JoinIntegDuplicateNameStep start = new JoinIntegDuplicateNameStep();

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(start);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(approvalResults, approvalCommands);
    }

    static final class JoinIntegDuplicateNameStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.gracefulComplete(input);
        }
    }
}
