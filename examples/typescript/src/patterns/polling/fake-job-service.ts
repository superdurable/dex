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

const FAKE_JOB_DURATION_MS = 6_000;

export type JobState = "QUEUED" | "RUNNING" | "SUCCEEDED" | "FAILED";

export interface JobStatus {
  readonly jobId: string;
  readonly state: JobState;
  readonly result?: string;
}

export class JobNotFoundError extends Error {
  public constructor(jobId: string) {
    super(`job ${jobId} not found`);
    this.name = "JobNotFoundError";
  }
}

// FakeJobService stands in for an external job API, such as a cloud build service.
export class FakeJobService {
  private readonly startedAtMsByJobId = new Map<string, number>();
  private readonly completedJobIds = new Set<string>();

  // startJob is idempotent per job ID, so a retried Step does not start a second job.
  public async startJob(jobId: string, signal: AbortSignal): Promise<void> {
    signal.throwIfAborted();
    if (!this.startedAtMsByJobId.has(jobId)) {
      this.startedAtMsByJobId.set(jobId, Date.now());
    }
  }

  public async getJobStatus(jobId: string, signal: AbortSignal): Promise<JobStatus> {
    signal.throwIfAborted();
    const startedAtMs = this.startedAtMsByJobId.get(jobId);
    if (startedAtMs === undefined) {
      throw new JobNotFoundError(jobId);
    }
    const elapsedMs = Date.now() - startedAtMs;
    if (this.completedJobIds.has(jobId) || elapsedMs >= FAKE_JOB_DURATION_MS) {
      return { jobId, state: "SUCCEEDED", result: `artifact for ${jobId}` };
    }
    if (elapsedMs >= FAKE_JOB_DURATION_MS / 3) {
      return { jobId, state: "RUNNING" };
    }
    return { jobId, state: "QUEUED" };
  }

  // completeJob finishes a job early so callers do not wait for its full duration.
  public completeJob(jobId: string): void {
    if (!this.startedAtMsByJobId.has(jobId)) {
      throw new JobNotFoundError(jobId);
    }
    this.completedJobIds.add(jobId);
  }
}
