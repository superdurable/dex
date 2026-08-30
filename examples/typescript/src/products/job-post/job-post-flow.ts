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
  Channel,
  IndexType,
  StepList,
  StepMovement,
  Wait,
  doubleCodec,
  goTo,
  goToMany,
  int64Codec,
  rpc,
  stringCodec,
  voidCodec,
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
  type MyDependencyService,
  myDependencyService,
} from "../../shared/my-dependency-service.js";
import {
  jobInfoCodec,
  optionalStringCodec,
  postingUpdateCodec,
  type JobInfo,
} from "./job-info.js";

const title = new Attribute("Title", stringCodec, {
  type: IndexType.FULL_TEXT,
});
const updatePostingLock = new Attribute("UpdatePostingLock", voidCodec);
const linkedInPostingUpdates = new Channel("LinkedInPostingUpdates", postingUpdateCodec);
const indeedPostingUpdates = new Channel("IndeedPostingUpdates", postingUpdateCodec);

export class JobPostingFlow implements Flow {
  public readonly title = title;
  public readonly jobDescription = new Attribute("JobDescription", stringCodec, {
    type: IndexType.FULL_TEXT,
  });
  public readonly lastUpdateTimeMillis = new Attribute("LastUpdateTimeMillis", int64Codec, {
    type: IndexType.INT,
  });
  public readonly notes = new Attribute("Notes", optionalStringCodec);
  public readonly updateVersion = new Attribute("UpdateVersion", doubleCodec);
  public readonly updatePostingLock = updatePostingLock;
  public readonly linkedInPostingUpdates = linkedInPostingUpdates;
  public readonly indeedPostingUpdates = indeedPostingUpdates;

  public readonly init = new InitStep();
  public readonly updateLinkedInPosting = new UpdateLinkedInPosting(this);
  public readonly updateIndeedPosting = new UpdateIndeedPosting(this);

  public constructor(public readonly service: MyDependencyService = myDependencyService) {}

  public getFlowType(): string {
    return "JobPostingFlow";
  }

  public getSteps() {
    return StepList.startStep(this.init).otherSteps(
      this.updateLinkedInPosting,
      this.updateIndeedPosting,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [
        this.title,
        this.jobDescription,
        this.lastUpdateTimeMillis,
        this.notes,
        this.updateVersion,
        this.updatePostingLock,
      ],
      channels: [this.linkedInPostingUpdates, this.indeedPostingUpdates],
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

  @rpc({
    inputCodec: jobInfoCodec,
    outputCodec: doubleCodec,
    lockAttributes: [updatePostingLock.lock()],
  })
  public update(context: Context, input: JobInfo): RPCResult<number> {
    const version = this.updateVersion.get(context) + 1;
    this.title.set(context, input.title);
    this.jobDescription.set(context, input.description);
    this.lastUpdateTimeMillis.set(context, BigInt(Date.now()));
    if (input.notes !== undefined && input.notes.length > 0) {
      this.notes.set(context, input.notes);
    }
    this.updateVersion.set(context, version);
    const update = {
      version,
      idempotencyKey: `${context.flowId}:${version}`,
      posting: input,
    };
    this.linkedInPostingUpdates.publish(context, update);
    this.indeedPostingUpdates.publish(context, update);
    return {
      output: version,
    };
  }

  private readJobInfo(context: Context): JobInfo {
    return {
      title: this.title.get(context),
      description: this.jobDescription.get(context),
      notes: this.notes.get(context) ?? "",
    };
  }
}

class InitStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "InitStep";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goToMany(
      StepMovement.of(UpdateLinkedInPosting, undefined),
      StepMovement.of(UpdateIndeedPosting, undefined),
    );
  }
}

class UpdateLinkedInPosting implements Step<void> {
  public constructor(private readonly flow: JobPostingFlow) {}

  public getStepType(): string {
    return "UpdateLinkedInPosting";
  }

  public getStepOptions(): StepOptions {
    return jobBoardUpdateOptions();
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(this.flow.linkedInPostingUpdates.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    const update = this.flow.linkedInPostingUpdates.results(context)[0];
    if (update === undefined) throw new Error("LinkedIn posting update is required");
    this.flow.service.updateExternalSystem(
      `update LinkedIn job posting v${update.version} `
      + `[${update.idempotencyKey}]: ${update.posting.title}`,
    );
    return goTo(UpdateLinkedInPosting, undefined);
  }
}

class UpdateIndeedPosting implements Step<void> {
  public constructor(private readonly flow: JobPostingFlow) {}

  public getStepType(): string {
    return "UpdateIndeedPosting";
  }

  public getStepOptions(): StepOptions {
    return jobBoardUpdateOptions();
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(this.flow.indeedPostingUpdates.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    const update = this.flow.indeedPostingUpdates.results(context)[0];
    if (update === undefined) throw new Error("Indeed posting update is required");
    this.flow.service.updateExternalSystem(
      `update Indeed job posting v${update.version} `
      + `[${update.idempotencyKey}]: ${update.posting.title}`,
    );
    return goTo(UpdateIndeedPosting, undefined);
  }
}

function jobBoardUpdateOptions(): StepOptions {
  return {
    executeRetry: {
      backoffCoefficient: 2,
      maximumAttempts: 100,
      totalDurationMs: HOUR_MS,
      initialIntervalMs: 3_000,
      maximumIntervalMs: 60_000,
    },
  };
}

export const jobPostingFlow = new JobPostingFlow();
