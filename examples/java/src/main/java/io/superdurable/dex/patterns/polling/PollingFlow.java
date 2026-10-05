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

package io.superdurable.dex.patterns.polling;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Stream;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.time.Instant;

@Component
public class PollingFlow implements Flow<Void> {
    // Keep POLL_INTERVAL + JOB_CALL_TIMEOUT <= HeartbeatTimeout - 10s.
    private static final Duration POLL_INTERVAL = Duration.ofSeconds(2);
    private static final Duration JOB_CALL_TIMEOUT = Duration.ofSeconds(10);
    private static final Duration JOB_WAIT_BUDGET = Duration.ofMinutes(5);

    public static final Stream<JobStatus> jobProgress =
            Stream.define("JobProgress", JobStatus.class, 1L << 20);

    private final FakeJobService jobs;
    private final StartJob startJob = new StartJob();
    private final AwaitJob awaitJob = new AwaitJob();

    public PollingFlow(final FakeJobService jobs) {
        this.jobs = jobs;
    }

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(startJob).otherSteps(awaitJob);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(jobProgress);
    }

    final class StartJob implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final String jobId = context.getFlowId();
            jobs.startJob(jobId, JOB_CALL_TIMEOUT);
            return StepDecision.goTo(AwaitJob.class, jobId);
        }
    }

    final class AwaitJob implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeMethodTimeout(Duration.ofMinutes(10))
                    .heartbeatTimeout(Duration.ofMinutes(1))
                    .executeRetry(RetryPolicy.newBuilder()
                            .initialInterval(Duration.ofSeconds(1))
                            .backoffCoefficient(2.0)
                            .maximumAttempts(3)
                            .totalDuration(Duration.ofMinutes(10))
                            .build())
                    .build();
        }

        @Override
        public StepDecision execute(final Context context, final String jobId) {
            final Instant deadline = context.getFirstAttemptAt().plus(JOB_WAIT_BUDGET);
            JobState reportedState = context.getLastHeartbeatValue(JobState.class);
            while (true) {
                final JobStatus status = jobs.getJobStatus(jobId, JOB_CALL_TIMEOUT);
                if (status.state() == JobState.SUCCEEDED) {
                    return StepDecision.gracefulComplete(status.result());
                }
                if (status.state() == JobState.FAILED) {
                    return StepDecision.forceFail("job " + jobId + " failed");
                }
                if (Instant.now().isAfter(deadline)) {
                    return StepDecision.forceFail(
                            "job " + jobId + " did not finish within " + JOB_WAIT_BUDGET);
                }
                if (status.state() != reportedState) {
                    jobProgress.write(context, status);
                    reportedState = status.state();
                } else {
                    context.recordHeartbeat(reportedState);
                }
                try {
                    Thread.sleep(POLL_INTERVAL.toMillis());
                } catch (final InterruptedException interrupted) {
                    Thread.currentThread().interrupt();
                    throw new IllegalStateException(interrupted);
                }
            }
        }
    }
}
