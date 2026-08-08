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

export type StateData = Readonly<Record<string, string>>;

export interface DealProcess {
  readonly processId: string;
  readonly initialState: string;
  readonly initialStateData: StateData;
  readonly states: readonly StateDefinition[];
}

export interface StateDefinition {
  readonly name: string;
  readonly preCondition?: ExternalCondition;
  readonly preActions: readonly string[];
  readonly postActions: readonly string[];
  readonly postCondition?: PostCondition;
}

export interface ExternalCondition {
  readonly name: string;
}

export interface PostCondition {
  readonly waitFor?: ExternalCondition;
  readonly decision: DecisionExpression;
}

export interface DecisionExpression {
  readonly key: string;
  readonly cases: readonly EqualCase[];
  readonly elseState: string;
}

export interface EqualCase {
  readonly equals: string;
  readonly goToState: string;
}

export interface StateStepInput {
  readonly stateName: string;
}

export type ActionPhase = "pre" | "post";

export interface ActionStepInput extends StateStepInput {
  readonly phase: ActionPhase;
}

const identifierPattern = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;

export function decodeDealProcess(value: unknown): DealProcess {
  const record = requireRecord(value, "deal process");
  const states = requireArray(record.states, "states").map(decodeStateDefinition);
  return {
    processId: requireString(record.processId, "processId"),
    initialState: requireString(record.initialState, "initialState"),
    initialStateData: decodeStateData(record.initialStateData ?? {}),
    states,
  };
}

export function decodeStateData(value: unknown): StateData {
  const record = requireRecord(value, "stateData");
  return Object.fromEntries(
    Object.entries(record).map(([key, entry]) => [key, requireString(entry, `stateData.${key}`)]),
  );
}

export function validateDealProcess(
  process: DealProcess,
  availableActionNames: readonly string[],
): void {
  requireIdentifier("processId", process.processId);
  if (process.states.length === 0) {
    throw new TypeError("deal process requires at least one state");
  }

  const states = new Map<string, StateDefinition>();
  for (const state of process.states) {
    requireIdentifier("state name", state.name);
    if (states.has(state.name)) {
      throw new TypeError(`duplicate state ${state.name}`);
    }
    states.set(state.name, state);
  }
  if (!states.has(process.initialState)) {
    throw new TypeError(`initial state ${process.initialState} is not defined`);
  }

  const conditions = new Map<string, string>();
  const actions = new Set(availableActionNames);
  for (const state of process.states) {
    validateState(state, states, conditions, actions);
  }
  for (const key of Object.keys(process.initialStateData)) {
    if (key.trim().length === 0) {
      throw new TypeError("initial stateData keys must not be empty");
    }
  }
}

export function stateByName(process: DealProcess, name: string): StateDefinition {
  const state = process.states.find((candidate) => candidate.name === name);
  if (state === undefined) {
    throw new TypeError(`state ${name} is not defined`);
  }
  return state;
}

export function hasCondition(process: DealProcess, name: string): boolean {
  return process.states.some(
    (state) => state.preCondition?.name === name || state.postCondition?.waitFor?.name === name,
  );
}

function decodeStateDefinition(value: unknown): StateDefinition {
  const record = requireRecord(value, "state");
  return {
    name: requireString(record.name, "state.name"),
    ...(record.preCondition === undefined
      ? {}
      : { preCondition: decodeExternalCondition(record.preCondition) }),
    preActions: decodeStringArray(record.preActions ?? [], "preActions"),
    postActions: decodeStringArray(record.postActions ?? [], "postActions"),
    ...(record.postCondition === undefined
      ? {}
      : { postCondition: decodePostCondition(record.postCondition) }),
  };
}

function decodeExternalCondition(value: unknown): ExternalCondition {
  const record = requireRecord(value, "external condition");
  return { name: requireString(record.name, "condition.name") };
}

function decodePostCondition(value: unknown): PostCondition {
  const record = requireRecord(value, "post condition");
  return {
    ...(record.waitFor === undefined
      ? {}
      : { waitFor: decodeExternalCondition(record.waitFor) }),
    decision: decodeDecision(record.decision),
  };
}

function decodeDecision(value: unknown): DecisionExpression {
  const record = requireRecord(value, "decision");
  return {
    key: requireString(record.key ?? "", "decision.key"),
    cases: requireArray(record.cases ?? [], "decision.cases").map(decodeEqualCase),
    elseState: requireString(record.elseState, "decision.elseState"),
  };
}

function decodeEqualCase(value: unknown): EqualCase {
  const record = requireRecord(value, "decision case");
  return {
    equals: requireString(record.equals, "case.equals"),
    goToState: requireString(record.goToState, "case.goToState"),
  };
}

function validateState(
  state: StateDefinition,
  states: ReadonlyMap<string, StateDefinition>,
  conditions: Map<string, string>,
  actions: ReadonlySet<string>,
): void {
  if (state.preCondition !== undefined) {
    validateCondition(state.name, state.preCondition, conditions);
  }
  for (const actionName of [...state.preActions, ...state.postActions]) {
    if (!actions.has(actionName)) {
      throw new TypeError(`state ${state.name}: action ${actionName} is not available`);
    }
  }
  if (state.postCondition === undefined) {
    return;
  }
  if (state.postCondition.waitFor !== undefined) {
    validateCondition(state.name, state.postCondition.waitFor, conditions);
  }
  validateDecision(state.name, state.postCondition.decision, states);
}

function validateCondition(
  stateName: string,
  condition: ExternalCondition,
  conditions: Map<string, string>,
): void {
  requireIdentifier("condition name", condition.name);
  const existingState = conditions.get(condition.name);
  if (existingState !== undefined) {
    throw new TypeError(`condition ${condition.name} is already used by state ${existingState}`);
  }
  conditions.set(condition.name, stateName);
}

function validateDecision(
  stateName: string,
  decision: DecisionExpression,
  states: ReadonlyMap<string, StateDefinition>,
): void {
  if (!states.has(decision.elseState)) {
    throw new TypeError(`state ${stateName}: else state ${decision.elseState} is not defined`);
  }
  if (decision.cases.length === 0) {
    return;
  }
  if (decision.key.trim().length === 0) {
    throw new TypeError(`state ${stateName}: decision key is required when cases are defined`);
  }
  const values = new Set<string>();
  for (const decisionCase of decision.cases) {
    if (values.has(decisionCase.equals)) {
      throw new TypeError(`state ${stateName}: duplicate equals value ${decisionCase.equals}`);
    }
    values.add(decisionCase.equals);
    if (!states.has(decisionCase.goToState)) {
      throw new TypeError(
        `state ${stateName}: case state ${decisionCase.goToState} is not defined`,
      );
    }
  }
}

function requireIdentifier(field: string, value: string): void {
  if (!identifierPattern.test(value)) {
    throw new TypeError(`${field} ${value} must match ${identifierPattern}`);
  }
}

function requireRecord(value: unknown, field: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new TypeError(`${field} must be an object`);
  }
  return value as Record<string, unknown>;
}

function requireArray(value: unknown, field: string): readonly unknown[] {
  if (!Array.isArray(value)) {
    throw new TypeError(`${field} must be an array`);
  }
  return value;
}

function decodeStringArray(value: unknown, field: string): readonly string[] {
  return requireArray(value, field).map((entry, index) =>
    requireString(entry, `${field}[${index}]`),
  );
}

function requireString(value: unknown, field: string): string {
  if (typeof value !== "string") {
    throw new TypeError(`${field} must be a string`);
  }
  return value;
}
