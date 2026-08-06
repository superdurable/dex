// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { PersistenceSchema } from "./persistence.js";
import { registeredRPCs } from "./rpc.js";
import { StepList, type Step } from "./step.js";
import { requireName } from "./validation.js";

export interface Flow<StartInput = void> {
  getFlowType(): string;
  getSteps(): StepList<StartInput>;
  getPersistenceSchema?(): PersistenceSchema;
}

export class Registry {
  public readonly flows: readonly Flow<any>[];

  public constructor(flows: readonly Flow<any>[]) {
    const flowNames = new Set<string>();
    for (const flow of flows) {
      const name = flowType(flow);
      if (flowNames.has(name)) {
        throw new TypeError(`duplicate Flow ${name}`);
      }
      flowNames.add(name);
      validateFlow(flow);
    }
    this.flows = Object.freeze([...flows]);
  }
}

function flowType(flow: Flow<any>): string {
  if (typeof flow.getFlowType !== "function") {
    throw new TypeError("Flow must implement getFlowType");
  }
  const name = flow.getFlowType();
  requireName(name);
  return name;
}

function stepType(step: Step<unknown>): string {
  if (typeof step.getStepType !== "function") {
    throw new TypeError("Step must implement getStepType");
  }
  const name = step.getStepType();
  requireName(name);
  return name;
}

function validateFlow(flow: Flow<any>): void {
  const definitions = flow.getSteps();
  if (!(definitions instanceof StepList)) {
    throw new TypeError("Flow steps must be a StepList");
  }
  const stepNames = new Set<string>();
  let hasStartStep = false;
  for (const definition of definitions) {
    if (definition.isStartStep) {
      if (hasStartStep) {
        throw new TypeError("Flow must not have multiple start Steps");
      }
      hasStartStep = true;
    }
    const step = definition.step;
    const name = stepType(step);
    if (stepNames.has(name)) {
      throw new TypeError(`duplicate Step ${name}`);
    }
    stepNames.add(name);
  }

  const rpcNames = new Set<string>();
  const attributes = new Set(flow.getPersistenceSchema?.().attributes ?? []);
  for (const registeredRPC of registeredRPCs(flow)) {
    if (rpcNames.has(registeredRPC.name)) {
      throw new TypeError(`duplicate RPC ${registeredRPC.name}`);
    }
    rpcNames.add(registeredRPC.name);
    if (
      registeredRPC.options.lockAttributes?.some((lock) => !attributes.has(lock.attribute)) === true
    ) {
      throw new TypeError(`RPC ${registeredRPC.name} locks an unregistered attribute`);
    }
  }
}
