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
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.SubFlow;
import io.superdurable.dex.SubFlowOptions;
import io.superdurable.dex.Wait;

// The start input is the SubFlow task queue; the output is "parent label>child label".
final class NamedTaskQueueParentWorkflow implements Flow<String> {
    private final String flowType;
    private final NamedTaskQueueParentStep parentStep;

    NamedTaskQueueParentWorkflow(final String flowType, final String workerLabel) {
        this.flowType = flowType;
        this.parentStep = new NamedTaskQueueParentStep(workerLabel);
    }

    @Override
    public String getFlowType() {
        return flowType;
    }

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(parentStep);
    }

    static final class NamedTaskQueueParentStep implements Step<String> {
        private final String workerLabel;

        NamedTaskQueueParentStep(final String workerLabel) {
            this.workerLabel = workerLabel;
        }

        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String subFlowTaskQueue) {
            return Wait.until(SubFlow.run(NamedTaskQueueWorkflow.class, "child",
                    SubFlowOptions.newBuilder().taskQueue(subFlowTaskQueue).build()));
        }

        @Override
        public StepDecision execute(final Context context, final String subFlowTaskQueue) {
            final FlowResult result = SubFlow.getConditionResults(context);
            return StepDecision.gracefulComplete(workerLabel + ">" + result.getSingleOutput(String.class));
        }
    }
}
