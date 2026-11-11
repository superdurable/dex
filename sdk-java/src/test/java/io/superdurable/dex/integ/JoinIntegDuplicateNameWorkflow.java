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
