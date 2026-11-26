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

import {
  ExecuteFailure,
  StepList,
  Timer,
  Wait,
  forceFail,
  goTo,
  gracefulComplete,
  jsonCodec,
  type AsyncContext,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

import type { FakeJobService } from "../polling/fake-job-service.js";

// POLL_INTERVAL_MS * MAX_POLL_ROUNDS bounds the whole wait.
const POLL_INTERVAL_MS = 10_000;
export const POLL_INTERVAL_TIMER_ID = "poll-interval";
const MAX_POLL_ROUNDS = 60;
const JOB_CALL_TIMEOUT_MS = 10_000;

export interface JobPollRound {
  jobId: string;
  roundNumber: number;
}

const jobPollRoundCodec = jsonCodec<JobPollRound>();

class RecordJobWaitFailure implements Step<JobPollRound> {
  public readonly inputCodec = jobPollRoundCodec;

  public getStepType(): string {
    return "RecordJobWaitFailure";
  }

  public execute(context: Context, pollRound: JobPollRound): StepDecision {
    const failure = context.recoveryError!;
    return forceFail(
      `checking job ${pollRound.jobId} failed: ${failure.errorType}: ${failure.detail}`,
    );
  }
}

class CheckJobStatus implements Step<JobPollRound> {
  public readonly inputCodec = jobPollRoundCodec;

  public constructor(private readonly jobs: FakeJobService) {}

  public getStepType(): string {
    return "CheckJobStatus";
  }

  public getStepOptions(): StepOptions {
    return {
      executeRetry: {
        initialIntervalMs: 1_000,
        backoffCoefficient: 2,
        maximumAttempts: 3,
      },
      executeFailure: ExecuteFailure.proceedTo(RecordJobWaitFailure),
    };
  }

  public waitFor(_context: Context, _pollRound: JobPollRound): Wait {
    return Wait.until(Timer.byDuration(POLL_INTERVAL_MS, POLL_INTERVAL_TIMER_ID));
  }

  public async execute(_context: AsyncContext, pollRound: JobPollRound): Promise<StepDecision> {
    const status = await this.jobs.getJobStatus(
      pollRound.jobId,
      AbortSignal.timeout(JOB_CALL_TIMEOUT_MS),
    );
    switch (status.state) {
      case "SUCCEEDED":
        return gracefulComplete(status.result);
      case "FAILED":
        return forceFail(`job ${pollRound.jobId} failed`);
    }
    if (pollRound.roundNumber >= MAX_POLL_ROUNDS) {
      return forceFail(`job ${pollRound.jobId} did not finish in ${MAX_POLL_ROUNDS} rounds`);
    }
    const nextRound = { jobId: pollRound.jobId, roundNumber: pollRound.roundNumber + 1 };
    return goTo(CheckJobStatus, nextRound);
  }
}

class StartJob implements Step<void> {
  public constructor(private readonly jobs: FakeJobService) {}

  public getStepType(): string {
    return "StartJob";
  }

  public async execute(context: Context, _input: void): Promise<StepDecision> {
    const jobId = context.flowId;
    await this.jobs.startJob(jobId, AbortSignal.timeout(JOB_CALL_TIMEOUT_MS));
    return goTo(CheckJobStatus, { jobId, roundNumber: 1 });
  }
}

export class PollingWithTimerFlow implements Flow<void> {
  private readonly startJob: StartJob;
  private readonly checkJobStatus: CheckJobStatus;
  private readonly recordJobWaitFailure = new RecordJobWaitFailure();

  public constructor(jobs: FakeJobService) {
    this.startJob = new StartJob(jobs);
    this.checkJobStatus = new CheckJobStatus(jobs);
  }

  public getFlowType(): string {
    return "PollingWithTimerFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.startJob).otherSteps(
      this.checkJobStatus,
      this.recordJobWaitFailure,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}
