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
  ConditionCombination,
  StepList,
  StepMovement,
  SubFlow,
  Wait,
  booleanCodec,
  doubleCodec,
  forceCompleteIfChannelsEmpty,
  goTo,
  goToMany,
  gracefulComplete,
  jsonCodec,
  rpc,
  stringCodec,
  voidCodec,
  type Codec,
  type Condition,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

import { getClient } from "../../client-holder.js";

const DEFAULT_CONCURRENCY = 10;
const MAX_BUFFERED_REQUESTS = 100;

export interface ParentInput {
  readonly requests: readonly string[];
  readonly concurrency: number;
}

export interface SubmitRequestInput {
  readonly request: string;
  readonly parentIds: readonly string[];
}

const parentInputCodec: Codec<ParentInput> = jsonCodec<ParentInput>({
  typeName: "ParentInput",
  decode: (value: unknown) => value as ParentInput,
});

const stringArrayCodec: Codec<readonly string[]> = jsonCodec<readonly string[]>({
  typeName: "string[]",
  decode: (value: unknown) => (Array.isArray(value) ? value.map(String) : []),
});

const submitRequestInputCodec: Codec<SubmitRequestInput> = jsonCodec<SubmitRequestInput>({
  typeName: "SubmitRequestInput",
  decode: (value: unknown) => value as SubmitRequestInput,
});

const requestChannel = new Channel("RequestChannel", stringCodec);

class DoWorkStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "DoWorkStep";
  }

  public async execute(_context: Context, request: string): Promise<StepDecision> {
    await new Promise((resolve) => setTimeout(resolve, 50 + (request.length % 10) * 50));
    return gracefulComplete(request);
  }
}

export class ExampleSubFlow implements Flow<string> {
  private readonly doWork = new DoWorkStep();

  public getFlowType(): string {
    return "ExampleSubFlow";
  }

  public getSteps() {
    return StepList.startStep(this.doWork);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

class SubFlowsStep implements Step<readonly string[]> {
  public readonly inputCodec = stringArrayCodec;

  public constructor(private readonly exampleSubFlow: ExampleSubFlow) {}

  public getStepType(): string {
    return "SubFlowsStep";
  }

  public waitFor(_context: Context, requests: readonly string[]): Wait {
    const conditions = requests.map((request, index) =>
      SubFlow.run(this.exampleSubFlow, request, { conditionId: `subflow-${index}` }),
    );
    return Wait.anyCombinationOf(
      ...combinations(conditions, Math.ceil(conditions.length / 2)).map((selected) =>
        ConditionCombination.of(...selected),
      ),
    );
  }

  public async execute(context: Context, requests: readonly string[]): Promise<StepDecision> {
    for (let index = 0; index < requests.length; index++) {
      if (SubFlow.getConditionResults(context, index).status === "running") {
        await getClient().stopFlow(SubFlow.getFlowId(context, index));
      }
    }
    return gracefulComplete();
  }
}

function combinations<T>(values: readonly T[], size: number): readonly (readonly T[])[] {
  const result: T[][] = [];
  const collect = (start: number, selected: readonly T[]): void => {
    if (selected.length === size) {
      result.push([...selected]);
      return;
    }
    for (let index = start; index <= values.length - (size - selected.length); index++) {
      const value = values[index];
      if (value !== undefined) collect(index + 1, [...selected, value]);
    }
  };
  collect(0, []);
  return result;
}

export class BasicParentFlow implements Flow<readonly string[]> {
  private readonly subFlows: SubFlowsStep;

  public constructor(exampleSubFlow: ExampleSubFlow) {
    this.subFlows = new SubFlowsStep(exampleSubFlow);
  }

  public getFlowType(): string {
    return "BasicParentFlow";
  }

  public getSteps() {
    return StepList.startStep(this.subFlows);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

class LongLiveInitStep implements Step<ParentInput> {
  public readonly inputCodec = parentInputCodec;

  public constructor(private readonly flow: AdvancedLongLiveParentFlow) {}

  public getStepType(): string {
    return "InitStep";
  }

  public execute(context: Context, input: ParentInput): StepDecision {
    for (const request of input.requests) requestChannel.publish(context, request);
    this.flow.stopped.set(context, false);
    const concurrency = input.concurrency > 0 ? input.concurrency : DEFAULT_CONCURRENCY;
    return goToMany(
      ...Array.from({ length: concurrency }, () =>
        StepMovement.of(LongLiveHandleRequestStep, undefined),
      ),
    );
  }
}

class LongLiveHandleRequestStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "HandleRequestStep";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(requestChannel.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    const request = requestChannel.results(context)[0];
    if (request === undefined) throw new Error("request is missing");
    return goTo(LongLiveHandleSubFlowStep, request);
  }
}

class LongLiveHandleSubFlowStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(
    private readonly flow: AdvancedLongLiveParentFlow,
    private readonly exampleSubFlow: ExampleSubFlow,
  ) {}

  public getStepType(): string {
    return "HandleSubFlowStep";
  }

  public waitFor(_context: Context, request: string): Wait {
    return Wait.until(SubFlow.run(this.exampleSubFlow, request));
  }

  public execute(context: Context, _request: string): StepDecision {
    return this.flow.stopped.get(context) === true
      ? gracefulComplete()
      : goTo(LongLiveHandleRequestStep, undefined);
  }
}

export class AdvancedLongLiveParentFlow implements Flow<ParentInput> {
  public readonly stopped = new Attribute("Stopped", booleanCodec);
  private readonly init = new LongLiveInitStep(this);
  private readonly handleRequest = new LongLiveHandleRequestStep();
  private readonly handleSubFlow: LongLiveHandleSubFlowStep;

  public constructor(exampleSubFlow: ExampleSubFlow) {
    this.handleSubFlow = new LongLiveHandleSubFlowStep(this, exampleSubFlow);
  }

  public getFlowType(): string {
    return "AdvancedLongLiveParentFlow";
  }

  public getSteps() {
    return StepList.startStep(this.init).otherSteps(this.handleRequest, this.handleSubFlow);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.stopped], channels: [requestChannel] };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: booleanCodec })
  public sendRequest(context: Context, request: string): RPCResult<boolean> {
    if (requestChannel.size(context) >= MAX_BUFFERED_REQUESTS) return { output: false };
    requestChannel.publish(context, request);
    return { output: true };
  }

  @rpc()
  public stop(context: Context): void {
    this.stopped.set(context, true);
  }
}

class ShortLiveInitStep implements Step<ParentInput> {
  public readonly inputCodec = parentInputCodec;

  public constructor(private readonly flow: AdvancedShortLiveParentFlow) {}

  public getStepType(): string {
    return "InitStep";
  }

  public execute(context: Context, input: ParentInput): StepDecision {
    for (const request of input.requests) requestChannel.publish(context, request);
    this.flow.currSubFlowNum.set(context, 0);
    const concurrency = input.concurrency > 0 ? input.concurrency : DEFAULT_CONCURRENCY;
    return goToMany(
      ...Array.from({ length: concurrency }, () =>
        StepMovement.of(ShortLiveHandleRequestStep, undefined),
      ),
    );
  }
}

class ShortLiveHandleRequestStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: AdvancedShortLiveParentFlow) {}

  public getStepType(): string {
    return "HandleRequestStep";
  }

  public getStepOptions(): StepOptions {
    return { executeLockAttributes: [this.flow.currSubFlowNum.lock()] };
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(requestChannel.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    const request = requestChannel.results(context)[0];
    if (request === undefined) throw new Error("request is missing");
    this.flow.currSubFlowNum.set(context, (this.flow.currSubFlowNum.get(context) ?? 0) + 1);
    return goTo(ShortLiveHandleSubFlowStep, request);
  }
}

class ShortLiveHandleSubFlowStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(
    private readonly flow: AdvancedShortLiveParentFlow,
    private readonly exampleSubFlow: ExampleSubFlow,
  ) {}

  public getStepType(): string {
    return "HandleSubFlowStep";
  }

  public getStepOptions(): StepOptions {
    return { executeLockAttributes: [this.flow.currSubFlowNum.lock()] };
  }

  public waitFor(_context: Context, request: string): Wait {
    return Wait.until(SubFlow.run(this.exampleSubFlow, request));
  }

  public execute(context: Context, _request: string): StepDecision {
    const current = (this.flow.currSubFlowNum.get(context) ?? 0) - 1;
    this.flow.currSubFlowNum.set(context, current);
    if (current === 0) {
      return forceCompleteIfChannelsEmpty(
        null,
        StepMovement.of(ShortLiveHandleRequestStep, undefined),
        requestChannel,
      );
    }
    return goTo(ShortLiveHandleRequestStep, undefined);
  }
}

export class AdvancedShortLiveParentFlow implements Flow<ParentInput> {
  public readonly currSubFlowNum = new Attribute("CurrSubFlowNum", doubleCodec);
  private readonly init = new ShortLiveInitStep(this);
  private readonly handleRequest = new ShortLiveHandleRequestStep(this);
  private readonly handleSubFlow: ShortLiveHandleSubFlowStep;

  public constructor(exampleSubFlow: ExampleSubFlow) {
    this.handleSubFlow = new ShortLiveHandleSubFlowStep(this, exampleSubFlow);
  }

  public getFlowType(): string {
    return "AdvancedShortLiveParentFlow";
  }

  public getSteps() {
    return StepList.startStep(this.init).otherSteps(this.handleRequest, this.handleSubFlow);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.currSubFlowNum], channels: [requestChannel] };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: booleanCodec })
  public sendRequest(context: Context, request: string): RPCResult<boolean> {
    if (requestChannel.size(context) >= MAX_BUFFERED_REQUESTS) return { output: false };
    requestChannel.publish(context, request);
    return { output: true };
  }
}

class SubmitStep implements Step<SubmitRequestInput> {
  public readonly inputCodec = submitRequestInputCodec;

  public constructor(private readonly parentFlow: AdvancedShortLiveParentFlow) {}

  public getStepType(): string {
    return "SubmitStep";
  }

  public async execute(_context: Context, input: SubmitRequestInput): Promise<StepDecision> {
    if (input.parentIds.length === 0) throw new Error("parent Flow IDs are required");
    const parentId = input.parentIds[partition(input.request, input.parentIds.length)];
    if (parentId === undefined) throw new Error("parent Flow ID is missing");
    const accepted = await getClient().invokeRPC(this.parentFlow.sendRequest, parentId, input.request);
    if (!accepted) throw new Error(`parent ${parentId} rejected the request`);
    return gracefulComplete(parentId);
  }
}

function partition(request: string, partitions: number): number {
  let hash = 2_166_136_261;
  for (const byte of new TextEncoder().encode(request)) {
    hash ^= byte;
    hash = Math.imul(hash, 16_777_619) >>> 0;
  }
  return hash % partitions;
}

export class SubmitRequestFlow implements Flow<SubmitRequestInput> {
  private readonly submit: SubmitStep;

  public constructor(parentFlow: AdvancedShortLiveParentFlow) {
    this.submit = new SubmitStep(parentFlow);
  }

  public getFlowType(): string {
    return "SubmitRequestFlow";
  }

  public getSteps() {
    return StepList.startStep(this.submit);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const exampleSubFlow = new ExampleSubFlow();
export const basicParentFlow = new BasicParentFlow(exampleSubFlow);
export const advancedLongLiveParentFlow = new AdvancedLongLiveParentFlow(exampleSubFlow);
export const advancedShortLiveParentFlow = new AdvancedShortLiveParentFlow(exampleSubFlow);
export const submitRequestFlow = new SubmitRequestFlow(advancedShortLiveParentFlow);
