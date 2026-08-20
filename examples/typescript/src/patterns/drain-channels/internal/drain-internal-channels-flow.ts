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
  StepList,
  StepMovement,
  Wait,
  doubleCodec,
  goTo,
  goToMulti,
  gracefulComplete,
  jsonCodec,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import {
  serviceDependency,
  type ServiceDependency,
} from "../../shared/service-dependency.js";
import {
  mongoDocumentCodec,
  type MongoDocument,
} from "./mongo-document.js";

export const UPSERT_MONGO_DATA_INTERNAL_CHANNEL = "upsert_mongo_data_internal_channel";
export const PROCESS_DATA_STATE_EXECUTION_COUNTER = "process_data_state_execution_counter";

const mongoDocumentInputCodec = jsonCodec<MongoDocument>(mongoDocumentCodec);
const stringInputCodec = stringCodec;

const upsertMongoData = new Channel(
  UPSERT_MONGO_DATA_INTERNAL_CHANNEL,
  mongoDocumentInputCodec,
);

class Init implements Step<string> {
  public readonly inputCodec = stringInputCodec;

  public constructor(private readonly flow: DrainInternalChannelsFlow) {}

  public getStepType(): string {
    return "Init";
  }

  public execute(context: Context, input: string): StepDecision {
    this.flow.processDataStateExecutionCounter.set(context, 0);
    return goToMulti(
      StepMovement.of(this.flow.upsertMongoRecordStep, undefined),
      StepMovement.of(this.flow.processDataStep, input),
    );
  }
}

class UpsertMongoRecord implements Step<void> {
  public constructor(private readonly flow: DrainInternalChannelsFlow) {}

  public getStepType(): string {
    return "UpsertMongoRecord";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(upsertMongoData.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    const documents = upsertMongoData.results(context);
    if (documents.length === 0) {
      throw new Error("No document was sent");
    }

    const document = documents[0];
    if (document === undefined) {
      throw new Error("No data was sent");
    }

    this.flow.mongoCollection.upsert(document);

    if (document.finalCommand) {
      return gracefulComplete();
    }
    return goTo(this.flow.upsertMongoRecordStep, undefined);
  }
}

class ProcessData implements Step<string> {
  public readonly inputCodec = stringInputCodec;

  public constructor(private readonly flow: DrainInternalChannelsFlow) {}

  public getStepType(): string {
    return "ProcessData";
  }

  public execute(context: Context, input: string): StepDecision {
    const executionCount = this.flow.processDataStateExecutionCounter.get(context) + 1;
    this.flow.processDataStateExecutionCounter.set(context, executionCount);

    let status: string;
    switch (executionCount) {
      case 1:
        status = "RECEIVED";
        break;
      case 2:
        status = "ACCEPTED";
        break;
      case 3:
        status = "PASSED";
        break;
      default:
        status = "ERROR";
        break;
    }

    upsertMongoData.publish(context, {
      id: input,
      status,
      finalCommand: false,
    });

    this.flow.externalService.externalApiCall(
      "external service call to process data (e.g. notify the job seeker)",
    );
    this.flow.externalService.externalApiCall(
      "a call to send metrics or add a log to logrepo",
    );

    if (executionCount <= 3) {
      return goTo(this.flow.processDataStep, input);
    }
    return goTo(this.flow.finalizeStep, undefined);
  }
}

class Finalize implements Step<void> {
  public constructor(private readonly flow: DrainInternalChannelsFlow) {}

  public getStepType(): string {
    return "Finalize";
  }

  public execute(context: Context, _input: void): StepDecision {
    upsertMongoData.publish(context, {
      id: "documentId-1",
      status: "FINALIZED",
      finalCommand: true,
    });
    return gracefulComplete();
  }
}

export class DrainInternalChannelsFlow implements Flow<string> {
  public readonly processDataStateExecutionCounter = new Attribute(
    PROCESS_DATA_STATE_EXECUTION_COUNTER,
    doubleCodec,
  );

  private readonly initStep = new Init(this);
  private readonly upsertMongoRecord = new UpsertMongoRecord(this);
  private readonly processData = new ProcessData(this);
  private readonly finalize = new Finalize(this);

  public constructor(
    public readonly externalService: ServiceDependency = serviceDependency,
    public readonly mongoCollection: ServiceDependency = serviceDependency,
  ) {}

  public get upsertMongoRecordStep(): Step<void> {
    return this.upsertMongoRecord;
  }

  public get processDataStep(): Step<string> {
    return this.processData;
  }

  public get finalizeStep(): Step<void> {
    return this.finalize;
  }

  public getFlowType(): string {
    return "DrainInternalChannelsFlow";
  }

  public getSteps() {
    return StepList.startStep(this.initStep).otherSteps(
      this.upsertMongoRecord,
      this.processData,
      this.finalize,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.processDataStateExecutionCounter],
      channels: [upsertMongoData],
    };
  }
}

export const drainInternalChannelsFlow = new DrainInternalChannelsFlow();
