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
  Timer,
  Wait,
  booleanCodec,
  goTo,
  gracefulComplete,
  jsonCodec,
  rpc,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

export const TOTAL_RECORDS = 1_000;
export const DEFAULT_BATCH_SIZE = 10;
export const MAX_BATCH_SIZE = 1_000;
export const BATCH_INTERVAL_MS = 1_000;

export const batchSize = new Attribute("batchSize", jsonCodec<number>());
export const stopRequested = new Attribute("stopRequested", booleanCodec);

/** The range of records that the loop has not processed yet. */
export interface BackfillProgress {
  readonly nextRecord: number;
  readonly lastRecord: number;
}

// dex:group group-id:start group-label:"Start"
// dex:explanation text:"Start the backfill at the first record."
class StartRecordBackfill implements Step<void> {
  public getStepType(): string {
    return "StartRecordBackfill";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goTo(ProcessRecordBatch, { nextRecord: 1, lastRecord: TOTAL_RECORDS });
  }
}

// dex:group group-id:batches group-label:"Batches"
// dex:explanation text:"Read the controls, then process one batch of records or stop."
class ProcessRecordBatch implements Step<BackfillProgress> {
  public getStepType(): string {
    return "ProcessRecordBatch";
  }

  public waitFor(_context: Context, _progress: BackfillProgress): Wait {
    return Wait.until(Timer.byDuration(BATCH_INTERVAL_MS));
  }

  public execute(context: Context, progress: BackfillProgress): StepDecision {
    if (stopRequested.get(context) === true) {
      return gracefulComplete(`stopped before record ${progress.nextRecord}`);
    }
    const size = batchSize.get(context) ?? DEFAULT_BATCH_SIZE;
    const last = Math.min(progress.nextRecord + size - 1, progress.lastRecord);
    console.log(`processing records ${progress.nextRecord} to ${last}`);
    if (last === progress.lastRecord) {
      return gracefulComplete(`processed ${last} records`);
    }
    return goTo(ProcessRecordBatch, {
      nextRecord: last + 1,
      lastRecord: progress.lastRecord,
    });
  }
}

/** Processes records in batches that two control Attributes steer. */
export class RecordBackfillFlow implements Flow<void> {
  private readonly startRecordBackfill = new StartRecordBackfill();
  private readonly processRecordBatch = new ProcessRecordBatch();

  public getFlowType(): string {
    return "RecordBackfillFlow";
  }

  public getSteps() {
    return StepList.startStep(this.startRecordBackfill).otherSteps(
      this.processRecordBatch,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [batchSize, stopRequested] };
  }

  /** Changes how many records each later batch processes. */
  @rpc({ inputCodec: jsonCodec<number>() })
  public setBatchSize(context: Context, size: number): void {
    if (!Number.isInteger(size) || size < 1 || size > MAX_BATCH_SIZE) {
      throw new Error(`batch size must be between 1 and ${MAX_BATCH_SIZE}`);
    }
    batchSize.set(context, size);
  }

  /** Asks the loop to stop before its next batch. */
  @rpc()
  public requestStop(context: Context): void {
    stopRequested.set(context, true);
  }
}

export const recordBackfillFlow = new RecordBackfillFlow();
