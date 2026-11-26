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

package io.superdurable.dex.patterns.pollingwithtimer;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RecoveryErrorInfo;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import io.superdurable.dex.patterns.polling.FakeJobService;
import io.superdurable.dex.patterns.polling.JobState;
import io.superdurable.dex.patterns.polling.JobStatus;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class PollingWithTimerFlow implements Flow<Void> {
    // MAX_POLL_ROUNDS bounds the number of checks, not elapsed time; calls, retries, and queueing add time.
    private static final Duration POLL_INTERVAL = Duration.ofSeconds(10);
    public static final String POLL_INTERVAL_TIMER_ID = "poll-interval";
    private static final int MAX_POLL_ROUNDS = 60;
    private static final Duration JOB_CALL_TIMEOUT = Duration.ofSeconds(10);

    private final FakeJobService jobs;
    private final StartJob startJob = new StartJob();
    private final CheckJobStatus checkJobStatus = new CheckJobStatus();
    private final RecordJobWaitFailure recordJobWaitFailure = new RecordJobWaitFailure();

    public PollingWithTimerFlow(final FakeJobService jobs) {
        this.jobs = jobs;
    }

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(startJob).otherSteps(checkJobStatus, recordJobWaitFailure);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
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
            return StepDecision.goTo(CheckJobStatus.class, new JobPollRound(jobId, 1));
        }
    }

    final class CheckJobStatus implements Step<JobPollRound> {
        @Override
        public Class<JobPollRound> getInputType() {
            return JobPollRound.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeRetry(RetryPolicy.newBuilder()
                            .initialInterval(Duration.ofSeconds(1))
                            .backoffCoefficient(2.0)
                            .maximumAttempts(3)
                            .build())
                    .onExecuteFailureProceedTo(RecordJobWaitFailure.class)
                    .build();
        }

        @Override
        public Wait waitFor(final Context context, final JobPollRound pollRound) {
            return Wait.until(Timer.byDuration(POLL_INTERVAL, POLL_INTERVAL_TIMER_ID));
        }

        @Override
        public StepDecision execute(final Context context, final JobPollRound pollRound) {
            final JobStatus status = jobs.getJobStatus(pollRound.jobId(), JOB_CALL_TIMEOUT);
            if (status.state() == JobState.SUCCEEDED) {
                return StepDecision.gracefulComplete(status.result());
            }
            if (status.state() == JobState.FAILED) {
                return StepDecision.forceFail("job " + pollRound.jobId() + " failed");
            }
            if (pollRound.roundNumber() >= MAX_POLL_ROUNDS) {
                return StepDecision.forceFail("job " + pollRound.jobId()
                        + " did not finish in " + MAX_POLL_ROUNDS + " rounds");
            }
            final JobPollRound nextRound =
                    new JobPollRound(pollRound.jobId(), pollRound.roundNumber() + 1);
            return StepDecision.goTo(CheckJobStatus.class, nextRound);
        }
    }

    final class RecordJobWaitFailure implements Step<JobPollRound> {
        @Override
        public Class<JobPollRound> getInputType() {
            return JobPollRound.class;
        }

        @Override
        public StepDecision execute(final Context context, final JobPollRound pollRound) {
            final RecoveryErrorInfo failure = context.getRecoveryError();
            return StepDecision.forceFail("checking job " + pollRound.jobId() + " failed: "
                    + failure.getErrorType() + ": " + failure.getDetail());
        }
    }
}
