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
  StepList,
  Wait,
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

export interface DealCondition {
  readonly name: string;
}

export interface DealCase {
  readonly equals: string;
  readonly goToState: string;
}

export interface DealTransition {
  readonly elseState: string;
  readonly waitFor?: DealCondition;
  readonly key?: string;
  readonly cases?: readonly DealCase[];
}

export interface DealState {
  readonly name: string;
  readonly preCondition?: DealCondition;
  readonly actions?: readonly string[];
  readonly transition?: DealTransition;
}

export interface DealDefinition {
  readonly processId: string;
  readonly itemId: string;
  readonly itemName: string;
  readonly initialState: string;
  readonly initialStateData: Readonly<Record<string, string>>;
  readonly states: readonly DealState[];
}

export interface DealStart {
  readonly definition: DealDefinition;
  readonly buyerId: string;
}

interface StateStepInput {
  readonly stateName: string;
}

interface ActionStepInput extends StateStepInput {
  readonly actionIndex: number;
}

const dealDefinitionCodec = jsonCodec<DealDefinition>();
const dealStartCodec = jsonCodec<DealStart>();
const stateDataCodec = jsonCodec<Record<string, string>>();
const stateStepInputCodec = jsonCodec<StateStepInput>();
const actionStepInputCodec = jsonCodec<ActionStepInput>();

export class DealDSLFlow implements Flow<DealStart> {
  public readonly definition = new Attribute("DealDefinition", dealDefinitionCodec);
  public readonly stateData = new Attribute("DealStateData", stateDataCodec);
  public readonly processId = new Attribute("DealProcessID", stringCodec);
  public readonly itemId = new Attribute("DealItemID", stringCodec);
  public readonly buyerId = new Attribute("DealBuyerID", stringCodec);
  public readonly currentState = new Attribute("DealCurrentState", stringCodec);
  public readonly pendingCondition = new Attribute("DealPendingCondition", stringCodec);
  public readonly conditionMessages = new ChannelMap("DealConditionMessages", stateDataCodec);

  public readonly initialize = new InitializeDeal(this);
  public readonly waitForCondition = new WaitForDealCondition(this);
  public readonly executeActionStep = new ExecuteDealAction(this);
  public readonly evaluateTransition = new EvaluateDealTransition(this);

  public getFlowType(): string {
    return "DealDSLFlow";
  }

  public getSteps() {
    return StepList.startStep(this.initialize).otherSteps(
      this.waitForCondition,
      this.executeActionStep,
      this.evaluateTransition,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [
        this.definition,
        this.stateData,
        this.processId,
        this.itemId,
        this.buyerId,
        this.currentState,
        this.pendingCondition,
      ],
      channels: [this.conditionMessages],
    };
  }

  public state(context: Context, stateName: string): DealState {
    const state = this.definition.get(context).states.find(({ name }) => name === stateName);
    if (state === undefined) throw new Error(`deal state ${stateName} is not defined`);
    return state;
  }

  public mergeCondition(context: Context, conditionName: string): void {
    const messages = this.conditionMessages.results(context, conditionName);
    if (messages.length !== 1) {
      throw new Error(`condition ${conditionName} requires one message`);
    }
    this.stateData.set(context, {
      ...this.stateData.get(context),
      ...messages[0],
    });
  }

  public executeAction(context: Context, actionName: string): void {
    const stateData = { ...this.stateData.get(context) };
    if (actionName === "deliverItemToBuyer") {
      stateData.itemDeliveryStatus = "delivered";
    } else if (actionName !== "chargeBuyer") {
      throw new Error(`deal action ${actionName} is not registered`);
    }
    stateData.lastAction = actionName;
    this.stateData.set(context, stateData);
  }
}

class InitializeDeal implements Step<DealStart> {
  public readonly inputCodec = dealStartCodec;

  public constructor(private readonly flow: DealDSLFlow) {}

  public getStepType(): string {
    return "InitializeDeal";
  }

  public execute(context: Context, input: DealStart): StepDecision {
    this.flow.definition.set(context, input.definition);
    this.flow.processId.set(context, input.definition.processId);
    this.flow.itemId.set(context, input.definition.itemId);
    this.flow.buyerId.set(context, input.buyerId);
    this.flow.stateData.set(context, { ...input.definition.initialStateData });
    this.flow.state(context, input.definition.initialState);
    return goTo(WaitForDealCondition, { stateName: input.definition.initialState });
  }
}

class WaitForDealCondition implements Step<StateStepInput> {
  public readonly inputCodec = stateStepInputCodec;

  public constructor(private readonly flow: DealDSLFlow) {}

  public getStepType(): string {
    return "WaitForDealCondition";
  }

  public waitFor(context: Context, input: StateStepInput): Wait {
    const condition = this.flow.state(context, input.stateName).preCondition;
    if (condition === undefined) return Wait.skipImmediately();
    this.flow.pendingCondition.set(context, condition.name);
    return Wait.until(this.flow.conditionMessages.forOne(condition.name));
  }

  public execute(context: Context, input: StateStepInput): StepDecision {
    const state = this.flow.state(context, input.stateName);
    if (state.preCondition !== undefined) {
      this.flow.mergeCondition(context, state.preCondition.name);
      this.flow.pendingCondition.delete(context);
    }
    this.flow.currentState.set(context, state.name);
    if ((state.actions?.length ?? 0) > 0) {
      return goTo(ExecuteDealAction, { stateName: state.name, actionIndex: 0 });
    }
    return goTo(EvaluateDealTransition, input);
  }
}

class ExecuteDealAction implements Step<ActionStepInput> {
  public readonly inputCodec = actionStepInputCodec;

  public constructor(private readonly flow: DealDSLFlow) {}

  public getStepType(): string {
    return "ExecuteDealAction";
  }

  public execute(context: Context, input: ActionStepInput): StepDecision {
    const state = this.flow.state(context, input.stateName);
    const action = state.actions?.[input.actionIndex];
    if (action === undefined) throw new Error(`invalid action index ${input.actionIndex}`);
    this.flow.executeAction(context, action);
    const nextIndex = input.actionIndex + 1;
    if (nextIndex < (state.actions?.length ?? 0)) {
      return goTo(ExecuteDealAction, { stateName: state.name, actionIndex: nextIndex });
    }
    return goTo(EvaluateDealTransition, { stateName: state.name });
  }
}

class EvaluateDealTransition implements Step<StateStepInput> {
  public readonly inputCodec = stateStepInputCodec;

  public constructor(private readonly flow: DealDSLFlow) {}

  public getStepType(): string {
    return "EvaluateDealTransition";
  }

  public waitFor(context: Context, input: StateStepInput): Wait {
    const condition = this.flow.state(context, input.stateName).transition?.waitFor;
    if (condition === undefined) return Wait.skipImmediately();
    this.flow.pendingCondition.set(context, condition.name);
    return Wait.until(this.flow.conditionMessages.forOne(condition.name));
  }

  public execute(context: Context, input: StateStepInput): StepDecision {
    const transition = this.flow.state(context, input.stateName).transition;
    if (transition === undefined) return gracefulComplete(this.flow.stateData.get(context));
    if (transition.waitFor !== undefined) {
      this.flow.mergeCondition(context, transition.waitFor.name);
      this.flow.pendingCondition.delete(context);
    }
    const stateData = this.flow.stateData.get(context);
    const matched = transition.cases?.find(
      (dealCase) => stateData[transition.key ?? ""] === dealCase.equals,
    );
    return goTo(WaitForDealCondition, {
      stateName: matched?.goToState ?? transition.elseState,
    });
  }
}

export function exampleDealStart(buyerId: string): DealStart {
  return {
    buyerId,
    definition: {
      processId: "item-deal-v1",
      itemId: "item-42",
      itemName: "Any sellable item",
      initialState: "negotiating",
      initialStateData: { accepted: "false" },
      states: [
        {
          name: "negotiating",
          transition: {
            waitFor: { name: "buyer-decision" },
            key: "accepted",
            cases: [{ equals: "true", goToState: "fulfill" }],
            elseState: "declined",
          },
        },
        { name: "fulfill", actions: ["chargeBuyer", "deliverItemToBuyer"] },
        { name: "declined" },
      ],
    },
  };
}

export const dealDSLFlow = new DealDSLFlow();
