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
  ChannelMap,
  IndexType,
  StepList,
  Wait,
  doubleCodec,
  goTo,
  gracefulComplete,
  jsonCodec,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import { DatasetDealActions } from "./actions.js";
import {
  decodeDealProcess,
  decodeStateData,
  stateByName,
  validateDealProcess,
  type ActionPhase,
  type ActionStepInput,
  type DealProcess,
  type DecisionExpression,
  type StateData,
  type StateDefinition,
  type StateStepInput,
} from "./models.js";

export const PROCESS_ID_SEARCH_KEY = "ProcessID";
export const BUYER_ID_SEARCH_KEY = "BuyerID";
export const CURRENT_STATE_SEARCH_KEY = "CurrentState";
export const PENDING_PRE_CONDITION_STATE_SEARCH_KEY = "PendingPreConditionState";
export const PENDING_PRE_CONDITION_NAME_SEARCH_KEY = "PendingPreConditionName";

export const stateDataCodec = jsonCodec<StateData>({
  typeName: "DatasetDealStateData",
  decode: decodeStateData,
});

export const dealProcessCodec = jsonCodec<DealProcess>({
  typeName: "DatasetDealProcess",
  decode: decodeDealProcess,
});

const stateStepInputCodec = jsonCodec<StateStepInput>({
  typeName: "DatasetDealStateStepInput",
  decode(value) {
    const input = value as Partial<StateStepInput>;
    if (typeof input.stateName !== "string") {
      throw new TypeError("stateName must be a string");
    }
    return { stateName: input.stateName };
  },
});

const actionStepInputCodec = jsonCodec<ActionStepInput>({
  typeName: "DatasetDealActionStepInput",
  decode(value) {
    const input = value as Partial<ActionStepInput>;
    if (typeof input.stateName !== "string") {
      throw new TypeError("stateName must be a string");
    }
    if (input.phase !== "pre" && input.phase !== "post") {
      throw new TypeError("action phase must be pre or post");
    }
    return { stateName: input.stateName, phase: input.phase };
  },
});

export class DatasetDealFlow implements Flow<string> {
  public readonly stateData = new Attribute("stateData", stateDataCodec);
  public readonly processDefinition = new Attribute("processDefinition", dealProcessCodec);
  public readonly processId = new Attribute("processID", stringCodec, {
    type: IndexType.KEYWORD,
    indexKey: PROCESS_ID_SEARCH_KEY,
  });
  public readonly buyerId = new Attribute("buyerID", stringCodec, {
    type: IndexType.KEYWORD,
    indexKey: BUYER_ID_SEARCH_KEY,
  });
  public readonly currentState = new Attribute("currentState", stringCodec, {
    type: IndexType.KEYWORD,
    indexKey: CURRENT_STATE_SEARCH_KEY,
  });
  public readonly currentActionIndexToExecute = new Attribute(
    "currentActionIndexToExecute",
    doubleCodec,
  );
  public readonly pendingPreConditionState = new Attribute(
    "pendingPreConditionState",
    stringCodec,
    {
      type: IndexType.KEYWORD,
      indexKey: PENDING_PRE_CONDITION_STATE_SEARCH_KEY,
    },
  );
  public readonly pendingPreConditionName = new Attribute(
    "pendingPreConditionName",
    stringCodec,
    {
      type: IndexType.KEYWORD,
      indexKey: PENDING_PRE_CONDITION_NAME_SEARCH_KEY,
    },
  );
  public readonly conditionMessages = new ChannelMap("conditionMessages", stateDataCodec);

  public readonly initialize = new Initialize(this);
  public readonly preCondition = new PreCondition(this);
  public readonly executeAction = new ExecuteAction(this);
  public readonly postCondition = new PostConditionStep(this);

  public constructor(public readonly actions: DatasetDealActions = new DatasetDealActions()) {}

  public getFlowType(): string {
    return "DatasetDealFlow";
  }

  public getSteps() {
    return StepList.startStep(this.initialize).otherSteps(
      this.preCondition,
      this.executeAction,
      this.postCondition,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [
        this.stateData,
        this.processDefinition,
        this.processId,
        this.buyerId,
        this.currentState,
        this.currentActionIndexToExecute,
        this.pendingPreConditionState,
        this.pendingPreConditionName,
      ],
      channels: [this.conditionMessages],
    };
  }
}

class Initialize implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: DatasetDealFlow) {}

  public getStepType(): string {
    return "DatasetDealInitialize";
  }

  public execute(context: Context, processId: string): StepDecision {
    const process = this.flow.processDefinition.get(context);
    validateDealProcess(process, this.flow.actions.availableNames());
    if (process.processId !== processId) {
      throw new TypeError(
        `process definition ${process.processId} does not match start input ${processId}`,
      );
    }
    this.flow.buyerId.get(context);
    this.flow.processId.set(context, processId);
    this.flow.stateData.set(context, process.initialStateData);
    this.flow.currentActionIndexToExecute.set(context, 0);
    return goTo(this.flow.preCondition, { stateName: process.initialState });
  }
}

class PreCondition implements Step<StateStepInput> {
  public readonly inputCodec = stateStepInputCodec;

  public constructor(private readonly flow: DatasetDealFlow) {}

  public getStepType(): string {
    return "DatasetDealPreCondition";
  }

  public waitFor(context: Context, input: StateStepInput): Wait {
    const state = stateByName(this.flow.processDefinition.get(context), input.stateName);
    if (state.preCondition === undefined) {
      return Wait.skipImmediately();
    }
    this.flow.pendingPreConditionState.set(context, state.name);
    this.flow.pendingPreConditionName.set(context, state.preCondition.name);
    return Wait.allOf(this.flow.conditionMessages.forOne(state.preCondition.name));
  }

  public execute(context: Context, input: StateStepInput): StepDecision {
    const state = stateByName(this.flow.processDefinition.get(context), input.stateName);
    if (state.preCondition !== undefined) {
      mergeStateData(
        this.flow,
        context,
        conditionUpdates(this.flow, context, state.preCondition.name),
      );
      this.flow.pendingPreConditionState.delete(context);
      this.flow.pendingPreConditionName.delete(context);
    }
    this.flow.currentActionIndexToExecute.set(context, 0);
    if (state.preActions.length > 0) {
      return goTo(this.flow.executeAction, { stateName: state.name, phase: "pre" });
    }
    return goToState(this.flow, context, state);
  }
}

class ExecuteAction implements Step<ActionStepInput> {
  public readonly inputCodec = actionStepInputCodec;

  public constructor(private readonly flow: DatasetDealFlow) {}

  public getStepType(): string {
    return "DatasetDealExecuteAction";
  }

  public async execute(context: Context, input: ActionStepInput): Promise<StepDecision> {
    const state = stateByName(this.flow.processDefinition.get(context), input.stateName);
    const actions = actionsForPhase(state, input.phase);
    const actionIndex = this.flow.currentActionIndexToExecute.get(context);
    if (!Number.isInteger(actionIndex) || actionIndex < 0 || actionIndex >= actions.length) {
      throw new RangeError(
        `action index ${actionIndex} is invalid for ${input.phase} actions in state ${state.name}`,
      );
    }

    const updates = await this.flow.actions.execute(actions[actionIndex]!, {
      flowId: context.flowId,
      processId: this.flow.processId.get(context),
      buyerId: this.flow.buyerId.get(context),
      targetState: state.name,
      stateData: this.flow.stateData.get(context),
    });
    mergeStateData(this.flow, context, updates);

    const nextActionIndex = actionIndex + 1;
    this.flow.currentActionIndexToExecute.set(context, nextActionIndex);
    if (nextActionIndex < actions.length) {
      return goTo(this.flow.executeAction, input);
    }
    this.flow.currentActionIndexToExecute.set(context, 0);
    if (input.phase === "pre") {
      return goToState(this.flow, context, state);
    }
    return goTo(this.flow.postCondition, { stateName: state.name });
  }
}

class PostConditionStep implements Step<StateStepInput> {
  public readonly inputCodec = stateStepInputCodec;

  public constructor(private readonly flow: DatasetDealFlow) {}

  public getStepType(): string {
    return "DatasetDealPostCondition";
  }

  public waitFor(context: Context, input: StateStepInput): Wait {
    const state = stateByName(this.flow.processDefinition.get(context), input.stateName);
    if (state.postCondition?.waitFor === undefined) {
      return Wait.skipImmediately();
    }
    return Wait.allOf(this.flow.conditionMessages.forOne(state.postCondition.waitFor.name));
  }

  public execute(context: Context, input: StateStepInput): StepDecision {
    const state = stateByName(this.flow.processDefinition.get(context), input.stateName);
    if (state.postCondition === undefined) {
      return gracefulComplete(this.flow.stateData.get(context));
    }
    if (state.postCondition.waitFor !== undefined) {
      mergeStateData(
        this.flow,
        context,
        conditionUpdates(this.flow, context, state.postCondition.waitFor.name),
      );
    }
    const nextState = evaluateDecision(
      state.postCondition.decision,
      this.flow.stateData.get(context),
    );
    return goTo(this.flow.preCondition, { stateName: nextState });
  }
}

function goToState(
  flow: DatasetDealFlow,
  context: Context,
  state: StateDefinition,
): StepDecision {
  flow.currentState.set(context, state.name);
  flow.currentActionIndexToExecute.set(context, 0);
  if (state.postActions.length > 0) {
    return goTo(flow.executeAction, { stateName: state.name, phase: "post" });
  }
  return goTo(flow.postCondition, { stateName: state.name });
}

function actionsForPhase(state: StateDefinition, phase: ActionPhase): readonly string[] {
  if (phase === "pre") {
    return state.preActions;
  }
  if (phase === "post") {
    return state.postActions;
  }
  throw new TypeError(`unknown action phase ${phase}`);
}

function conditionUpdates(
  flow: DatasetDealFlow,
  context: Context,
  conditionName: string,
): StateData {
  const results = flow.conditionMessages.results(context, conditionName);
  if (results.length !== 1) {
    throw new TypeError(
      `condition ${conditionName} expected one message, received ${results.length}`,
    );
  }
  return results[0]!;
}

function mergeStateData(flow: DatasetDealFlow, context: Context, updates: StateData): void {
  for (const key of Object.keys(updates)) {
    if (key.trim().length === 0) {
      throw new TypeError("stateData update key must not be empty");
    }
  }
  flow.stateData.set(context, { ...flow.stateData.get(context), ...updates });
}

function evaluateDecision(decision: DecisionExpression, stateData: StateData): string {
  const value = stateData[decision.key];
  return (
    decision.cases.find((decisionCase) => decisionCase.equals === value)?.goToState ??
    decision.elseState
  );
}

export const datasetDealFlow = new DatasetDealFlow();
