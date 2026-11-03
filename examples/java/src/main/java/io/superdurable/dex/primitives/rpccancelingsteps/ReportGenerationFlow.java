/*
 * Copyright (c) 2026 Super Durable, Inc.
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

package io.superdurable.dex.primitives.rpccancelingsteps;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Stream;
import org.springframework.stereotype.Component;

import java.time.Duration;

/** An RPC that cancels a running Step: a user stops a report while it generates. */
@Component
public class ReportGenerationFlow implements Flow<String> {
    public static final String REPORT_READY = "ready";
    public static final String REPORT_STOPPED = "stopped";
    private static final int SECTION_COUNT = 10;
    private static final Duration SECTION_WORK = Duration.ofSeconds(1);

    public static final Attribute<String> reportStatus =
            Attribute.define("ReportStatus", String.class);
    public static final Stream<Integer> reportProgress =
            Stream.define("ReportProgress", Integer.class, 1L << 20);

    private final GenerateReport generateReport = new GenerateReport();
    private final DeliverReport deliverReport = new DeliverReport();
    private final FinishReportStop finishReportStop = new FinishReportStop();

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(generateReport).otherSteps(deliverReport, finishReportStop);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(reportStatus, reportProgress);
    }

    @RPC(timeoutSeconds = 30)
    public RPCResult<Boolean> stopReport(final Context context) {
        final String status = reportStatus.get(context);
        if (REPORT_READY.equals(status) || REPORT_STOPPED.equals(status)) {
            return RPCResult.of(false);
        }
        return RPCResult.of(true, StepMovement.of(FinishReportStop.class, null))
                .withCancelingSteps(GenerateReport.class);
    }

    static final class GenerateReport implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeMethodTimeout(Duration.ofMinutes(5))
                    .heartbeatTimeout(Duration.ofSeconds(30))
                    .build();
        }

        @Override
        public StepDecision execute(final Context context, final String reportName) {
            for (int section = 1; section <= SECTION_COUNT; section++) {
                try {
                    Thread.sleep(SECTION_WORK.toMillis());
                } catch (final InterruptedException interrupted) {
                    // A Stop interrupts this thread; the result of this attempt is discarded.
                    Thread.currentThread().interrupt();
                    throw new IllegalStateException("report generation canceled", interrupted);
                }
                reportProgress.write(context, section);
            }
            reportStatus.set(context, REPORT_READY);
            return StepDecision.goTo(DeliverReport.class, reportName);
        }
    }

    static final class DeliverReport implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String reportName) {
            return StepDecision.gracefulComplete(reportName + " delivered");
        }
    }

    static final class FinishReportStop implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            // The report can commit after stopReport read its snapshot.
            if (REPORT_READY.equals(reportStatus.get(context))) {
                return StepDecision.deadEnd();
            }
            reportStatus.set(context, REPORT_STOPPED);
            return StepDecision.gracefulComplete(REPORT_STOPPED);
        }
    }
}
