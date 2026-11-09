/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package io.superdurable.dex.patterns.workflowpatterns;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.ArrayList;
import java.util.List;

/** WCP-15: RPCs add instances; the join loops until intake closes and every instance reports. */
@Component
public class NoPriorKnowledgeMultipleInstancesFlow implements Flow<String[]> {
    public static final Attribute<Integer> startedSampleCount =
            Attribute.define("startedSampleCount", Integer.class);
    public static final Attribute<Boolean> isSampleIntakeClosed =
            Attribute.define("isSampleIntakeClosed", Boolean.class);
    public static final Channel<String> sampleAnalyzed =
            Channel.define("sampleAnalyzed", String.class);
    public static final AttributeMap<Boolean> analysisStartedSamples =
            AttributeMap.define("analysisStartedSamples", Boolean.class);
    public static final Channel<String> sampleIntakeClosed =
            Channel.define("sampleIntakeClosed", String.class);
    public static final Channel<String> sampleGateOpened =
            Channel.define("sampleGateOpened", String.class);

    private final JoinSamples joinSamples = new JoinSamples();
    private final AnalyzeSample analyzeSample = new AnalyzeSample();
    private final ReportSamples reportSamples = new ReportSamples();

    @Override
    public StepList<String[]> getSteps() {
        return StepList.startStep(joinSamples).otherSteps(analyzeSample, reportSamples);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                startedSampleCount,
                isSampleIntakeClosed,
                analysisStartedSamples,
                sampleAnalyzed,
                sampleIntakeClosed,
                sampleGateOpened);
    }

    /** Starts one more AnalyzeSample instance, or returns false once intake is closed. */
    @RPC(lockAttributes = {"startedSampleCount"})
    public RPCResult<Boolean> addSample(final Context context, final String sampleId) {
        if (isIntakeClosed(context)) {
            return RPCResult.of(false);
        }
        startedSampleCount.set(context, startedSamples(context) + 1);
        return RPCResult.of(true, StepMovement.of(AnalyzeSample.class, sampleId));
    }

    @RPC(lockAttributes = {"startedSampleCount"})
    public void closeSampleIntake(final Context context) {
        isSampleIntakeClosed.set(context, true);
        sampleIntakeClosed.publish(context, "closed");
    }

    /** Counts the AnalyzeSample instances that are in flight at the gate. */
    @RPC(loadAttributeMaps = {"analysisStartedSamples"})
    public RPCResult<Integer> getStartedAnalysisCount(final Context context) {
        return RPCResult.of(analysisStartedSamples.getMapSize(context));
    }

    /** Lets count waiting AnalyzeSample instances finish. */
    @RPC
    public void openSampleGate(final Context context, final Integer count) {
        for (int index = 0; index < count; index++) {
            sampleGateOpened.publish(context, "opened");
        }
    }

    private static boolean isIntakeClosed(final Context context) {
        return Boolean.TRUE.equals(isSampleIntakeClosed.get(context));
    }

    private static int startedSamples(final Context context) {
        final Integer started = startedSampleCount.get(context);
        return started == null ? 0 : started;
    }

    static final class JoinSamples implements Step<String[]> {
        @Override
        public Class<String[]> getInputType() {
            return String[].class;
        }

        @Override
        public Wait waitFor(final Context context, final String[] analyzedSamples) {
            if (!isIntakeClosed(context)) {
                return Wait.anyOf(sampleAnalyzed.forOne(), sampleIntakeClosed.forOne());
            }
            if (analyzedSamples.length == startedSamples(context)) {
                return Wait.skipImmediately();
            }
            return Wait.until(sampleAnalyzed.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final String[] analyzedSamples) {
            // No lock: startedSampleCount cannot change once intake is closed.
            final List<String> analyzed = new ArrayList<String>(List.of(analyzedSamples));
            analyzed.addAll(sampleAnalyzed.getConditionResults(context));
            final String[] updated = analyzed.toArray(new String[0]);
            if (isIntakeClosed(context) && analyzed.size() == startedSamples(context)) {
                return StepDecision.goTo(ReportSamples.class, updated);
            }
            return StepDecision.goTo(JoinSamples.class, updated);
        }
    }

    static final class AnalyzeSample implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String sampleId) {
            analysisStartedSamples.set(context, sampleId, true);
            return Wait.until(sampleGateOpened.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final String sampleId) {
            sampleAnalyzed.publish(context, sampleId);
            return StepDecision.deadEnd();
        }
    }

    static final class ReportSamples implements Step<String[]> {
        @Override
        public Class<String[]> getInputType() {
            return String[].class;
        }

        @Override
        public StepDecision execute(final Context context, final String[] analyzedSamples) {
            return StepDecision.gracefulComplete(List.of(analyzedSamples).stream().sorted().toList());
        }
    }
}
