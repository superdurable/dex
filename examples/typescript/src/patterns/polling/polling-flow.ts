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

import { setTimeout } from "node:timers/promises";

import {
  ExecuteFailure,
  StepList,
  Stream,
  forceFail,
  goTo,
  gracefulComplete,
  jsonCodec,
  stringCodec,
  type AsyncContext,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

import type { FakeJobService, JobState, JobStatus } from "./fake-job-service.js";

// Keep POLL_INTERVAL_MS + JOB_CALL_TIMEOUT_MS <= heartbeatTimeoutMs - 10s.
// MAX_JOB_WAIT_MS bounds one attempt and all retries; Dex enforces it.
const POLL_INTERVAL_MS = 2_000;
const JOB_CALL_TIMEOUT_MS = 10_000;
const MAX_JOB_WAIT_MS = 10 * 60_000;

export const jobProgress = new Stream("JobProgress", jsonCodec<JobStatus>(), 1 << 20);

class RecordJobWaitFailure implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "RecordJobWaitFailure";
  }

  public execute(context: Context, jobId: string): StepDecision {
    const failure = context.recoveryError!;
    return forceFail(`waiting for job ${jobId} failed: ${failure.errorType}: ${failure.detail}`);
  }
}

class AwaitJob implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly jobs: FakeJobService) {}

  public getStepType(): string {
    return "AwaitJob";
  }

  public getStepOptions(): StepOptions {
    return {
      executeMethodTimeoutMs: MAX_JOB_WAIT_MS,
      heartbeatTimeoutMs: 60_000,
      executeRetry: {
        initialIntervalMs: 1_000,
        backoffCoefficient: 2,
        maximumAttempts: 3,
        totalDurationMs: MAX_JOB_WAIT_MS,
      },
      executeFailure: ExecuteFailure.proceedTo(RecordJobWaitFailure),
    };
  }

  public async execute(context: AsyncContext, jobId: string): Promise<StepDecision> {
    let reportedState = context.getLastHeartbeatValue<JobState>();
    while (true) {
      const status = await this.jobs.getJobStatus(jobId, AbortSignal.timeout(JOB_CALL_TIMEOUT_MS));
      switch (status.state) {
        case "SUCCEEDED":
          return gracefulComplete(status.result);
        case "FAILED":
          return forceFail(`job ${jobId} failed`);
      }
      if (status.state !== reportedState) {
        jobProgress.write(context, status);
        reportedState = status.state;
      } else {
        await context.recordHeartbeat(reportedState);
      }
      await setTimeout(POLL_INTERVAL_MS);
    }
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
    return goTo(AwaitJob, jobId);
  }
}

export class PollingFlow implements Flow<void> {
  private readonly startJob: StartJob;
  private readonly awaitJob: AwaitJob;
  private readonly recordJobWaitFailure = new RecordJobWaitFailure();

  public constructor(jobs: FakeJobService) {
    this.startJob = new StartJob(jobs);
    this.awaitJob = new AwaitJob(jobs);
  }

  public getFlowType(): string {
    return "PollingFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.startJob).otherSteps(this.awaitJob, this.recordJobWaitFailure);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { streams: [jobProgress] };
  }
}
