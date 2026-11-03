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

// An RPC that cancels a running Step: a user stops a report while it generates.

import { setTimeout } from "node:timers/promises";

import {
  Attribute,
  StepList,
  StepMovement,
  Stream,
  booleanCodec,
  deadEnd,
  doubleCodec,
  goTo,
  gracefulComplete,
  rpc,
  stringCodec,
  voidCodec,
  type AsyncContext,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

export const REPORT_READY = "ready";
export const REPORT_STOPPED = "stopped";
const SECTION_COUNT = 10;
const SECTION_WORK_MS = 1_000;

export const reportStatus = new Attribute("ReportStatus", stringCodec);
export const reportProgress = new Stream("ReportProgress", doubleCodec, 1 << 20);

class GenerateReport implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "GenerateReport";
  }

  public getStepOptions(): StepOptions {
    return { executeMethodTimeoutMs: 5 * 60_000, heartbeatTimeoutMs: 30_000 };
  }

  public async execute(context: AsyncContext, reportName: string): Promise<StepDecision> {
    for (let section = 1; section <= SECTION_COUNT; section++) {
      // A Stop aborts the signal, so this await rejects with an AbortError.
      await setTimeout(SECTION_WORK_MS, undefined, { signal: context.cancellationSignal });
      reportProgress.write(context, section);
    }
    reportStatus.set(context, REPORT_READY);
    return goTo(DeliverReport, reportName);
  }
}

class DeliverReport implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "DeliverReport";
  }

  public execute(_context: Context, reportName: string): StepDecision {
    return gracefulComplete(`${reportName} delivered`);
  }
}

class FinishReportStop implements Step<void> {
  public getStepType(): string {
    return "FinishReportStop";
  }

  public execute(context: Context, _input: void): StepDecision {
    // The report can commit after stopReport read its snapshot.
    if (reportStatus.get(context) === REPORT_READY) {
      return deadEnd();
    }
    reportStatus.set(context, REPORT_STOPPED);
    return gracefulComplete(REPORT_STOPPED);
  }
}

export class ReportGenerationFlow implements Flow<string> {
  private readonly generateReport = new GenerateReport();
  private readonly deliverReport = new DeliverReport();
  private readonly finishReportStop = new FinishReportStop();

  public getFlowType(): string {
    return "ReportGenerationFlow";
  }

  public getSteps(): StepList<string> {
    return StepList.startStep(this.generateReport).otherSteps(
      this.deliverReport,
      this.finishReportStop,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [reportStatus], streams: [reportProgress] };
  }

  @rpc({ inputCodec: voidCodec, outputCodec: booleanCodec, timeoutMs: 30_000 })
  public stopReport(context: Context, _input: void): RPCResult<boolean> {
    const status = reportStatus.get(context);
    if (status === REPORT_READY || status === REPORT_STOPPED) {
      return { output: false };
    }
    return {
      output: true,
      nextSteps: [StepMovement.of(FinishReportStop, undefined)],
      cancelingSteps: [GenerateReport],
    };
  }
}

export const reportGenerationFlow = new ReportGenerationFlow();
