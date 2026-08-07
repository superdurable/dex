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
  StepList,
  goTo,
  gracefulComplete,
  jsonCodec,
  rpc,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import {
  serviceDependency,
  type ServiceDependency,
} from "../../services/service-dependency.js";
import {
  jobSeekerDataCodec,
  type JobSeekerData,
} from "./job-seeker-data.js";

export const JOB_SEEKER_DATA = "job_seeker_data";

const jobSeekerDataInputCodec = jsonCodec<JobSeekerData>(jobSeekerDataCodec);

class PersistData implements Step<JobSeekerData> {
  public readonly inputCodec = jobSeekerDataInputCodec;

  public constructor(private readonly flow: WaitForStateCompletionFlow) {}

  public getStepType(): string {
    return "PersistData";
  }

  public execute(context: Context, input: JobSeekerData): StepDecision {
    this.flow.mongoCollection.upsert(input);
    this.flow.jobSeekerData.set(context, input);
    return goTo(this.flow.updateExternalSystemStep, input);
  }
}

class UpdateExternalSystem implements Step<JobSeekerData> {
  public readonly inputCodec = jobSeekerDataInputCodec;

  public constructor(private readonly flow: WaitForStateCompletionFlow) {}

  public getStepType(): string {
    return "UpdateExternalSystem";
  }

  public execute(context: Context, input: JobSeekerData): StepDecision {
    this.flow.externalService.updateExternalSystem(JSON.stringify(input));
    return gracefulComplete();
  }
}

export class WaitForStateCompletionFlow implements Flow<JobSeekerData> {
  public readonly jobSeekerData = new Attribute(JOB_SEEKER_DATA, jobSeekerDataInputCodec);

  private readonly persistData: PersistData;
  private readonly updateExternalSystem: UpdateExternalSystem;

  public constructor(
    public readonly mongoCollection: ServiceDependency = serviceDependency,
    public readonly externalService: ServiceDependency = serviceDependency,
  ) {
    this.persistData = new PersistData(this);
    this.updateExternalSystem = new UpdateExternalSystem(this);
  }

  public get updateExternalSystemStep(): Step<JobSeekerData> {
    return this.updateExternalSystem;
  }

  public getFlowType(): string {
    return "WaitForStateCompletionFlow";
  }

  public getSteps() {
    return StepList.startStep(this.persistData).otherSteps(this.updateExternalSystem);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.jobSeekerData] };
  }

  @rpc({ outputCodec: jobSeekerDataInputCodec })
  public getJobSeekerData(context: Context): RPCResult<JobSeekerData> {
    const data = this.jobSeekerData.get(context);
    if (data === undefined) {
      throw new Error("Job seeker data was not persisted to the data store");
    }
    return { output: data };
  }
}

export const waitForStateCompletionFlow = new WaitForStateCompletionFlow();
