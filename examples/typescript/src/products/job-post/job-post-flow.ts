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
  Attribute,
  IndexType,
  StepList,
  StepMovement,
  deadEnd,
  int64Codec,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

import { HOUR_MS } from "../../config/env.js";
import {
  myDependencyService,
  type MyDependencyService,
} from "../../shared/my-dependency-service.js";
import { optionalStringCodec, type JobInfo } from "./job-info.js";

export class JobPostFlow implements Flow {
  public readonly title = new Attribute("Title", stringCodec, {
    type: IndexType.FULL_TEXT,
  });
  public readonly jobDescription = new Attribute("JobDescription", stringCodec, {
    type: IndexType.FULL_TEXT,
  });
  public readonly lastUpdateTimeMillis = new Attribute("LastUpdateTimeMillis", int64Codec, {
    type: IndexType.INT,
  });
  public readonly notes = new Attribute("Notes", optionalStringCodec);

  public readonly externalUpdate = new ExternalUpdate(this);

  public constructor(public readonly service: MyDependencyService = myDependencyService) {}

  public getFlowType(): string {
    return "JobPostFlow";
  }

  public getSteps() {
    return StepList.withoutStartStep<void>(this.externalUpdate);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.title, this.jobDescription, this.lastUpdateTimeMillis, this.notes],
    };
  }

  @rpc()
  public get(context: Context): RPCResult<JobInfo> {
    return { output: this.readJobInfo(context) };
  }

  @rpc({ name: "getWithStrongConsistency" })
  public getWithStrongConsistency(context: Context): RPCResult<JobInfo> {
    return this.get(context);
  }

  @rpc()
  public update(context: Context, input: JobInfo): RPCResult<void> {
    this.title.set(context, input.title);
    this.jobDescription.set(context, input.description);
    this.lastUpdateTimeMillis.set(context, BigInt(Date.now()));
    if (input.notes !== undefined && input.notes.length > 0) {
      this.notes.set(context, input.notes);
    }
    return { output: undefined, nextSteps: [StepMovement.of(ExternalUpdate, undefined)] };
  }

  private readJobInfo(context: Context): JobInfo {
    return {
      title: this.title.get(context),
      description: this.jobDescription.get(context),
      notes: this.notes.get(context) ?? "",
    };
  }
}

class ExternalUpdate implements Step<void> {
  private readonly options: StepOptions = {
    executeRetry: {
      backoffCoefficient: 2,
      maximumAttempts: 100,
      totalDurationMs: HOUR_MS,
      initialIntervalMs: 3_000,
      maximumIntervalMs: 60_000,
    },
  };

  public constructor(private readonly flow: JobPostFlow) {}

  public getStepType(): string {
    return "ExternalUpdate";
  }

  public getStepOptions(): StepOptions {
    return this.options;
  }

  public execute(_context: Context, _input: void): StepDecision {
    this.flow.service.updateExternalSystem("this is an update to external service");
    return deadEnd();
  }
}

export const jobPostFlow = new JobPostFlow();
