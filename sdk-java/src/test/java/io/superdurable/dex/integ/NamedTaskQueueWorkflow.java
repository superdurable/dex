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
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;

// Every Worker registers the same Flow type with its own label to reveal which Worker ran it.
final class NamedTaskQueueWorkflow implements Flow<String> {
    private final String flowType;
    private final NamedTaskQueueReportStep reportStep;

    NamedTaskQueueWorkflow(final String flowType, final String workerLabel) {
        this.flowType = flowType;
        this.reportStep = new NamedTaskQueueReportStep(workerLabel);
    }

    @Override
    public String getFlowType() {
        return flowType;
    }

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(reportStep);
    }

    static final class NamedTaskQueueReportStep implements Step<String> {
        private final String workerLabel;

        NamedTaskQueueReportStep(final String workerLabel) {
            this.workerLabel = workerLabel;
        }

        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.gracefulComplete(workerLabel);
        }
    }
}
