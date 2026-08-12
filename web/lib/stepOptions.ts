// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { FlowHistoryEvent } from './types';

type Data = Record<string, unknown>;

export function findSourceStepOptions(
  event: FlowHistoryEvent,
  history: FlowHistoryEvent[],
): Data {
  return doFindSourceStepOptions(event, history, new Set<number>());
}

function doFindSourceStepOptions(
  event: FlowHistoryEvent,
  history: FlowHistoryEvent[],
  visitedEventIDs: Set<number>,
): Data {
  if (visitedEventIDs.has(event.eventId)) return {};
  visitedEventIDs.add(event.eventId);

  const context = asData(event.payload.context);
  const stepExecutionID = String(context.stepExecutionId ?? '');
  const fromStepExecutionID = String(context.fromStepExecutionId ?? '');
  const stepType = String(context.stepType ?? '');
  const stepInput = asData(event.payload.input).stepInput;
  if (!stepExecutionID || !stepType) return {};

  const carriedOptions = findContinuedStepOptions(
    event,
    history,
    stepExecutionID,
    fromStepExecutionID,
    stepType,
    stepInput,
  );
  if (hasData(carriedOptions)) return carriedOptions;

  if (fromStepExecutionID === '__start__') {
    const initialOptions = findInitialStepOptions(event, history, stepType);
    if (hasData(initialOptions)) return initialOptions;
  }

  const sourceEvents = history
    .filter((candidate) => candidate.eventId < event.eventId)
    .filter((candidate) => isSourceEvent(candidate, fromStepExecutionID))
    .sort((left, right) => right.eventId - left.eventId);
  for (const sourceEvent of sourceEvents) {
    const movement = selectStepMovement(
      sourceMovements(sourceEvent),
      stepType,
      fromStepExecutionID,
      stepInput,
    );
    const movementOptions = asData(movement.stepOptions);
    if (hasData(movementOptions)) return movementOptions;

    if (sourceEvent.type === 'StepExecuteFailed') {
      const failedSourceOptions = doFindSourceStepOptions(sourceEvent, history, visitedEventIDs);
      if (failedSourceOptions.executeFailureProceedStepType === stepType) {
        const proceedOptions = asData(failedSourceOptions.executeFailureProceedStepOptions);
        if (hasData(proceedOptions)) return proceedOptions;
      }
    }
  }
  return {};
}

function findContinuedStepOptions(
  event: FlowHistoryEvent,
  history: FlowHistoryEvent[],
  stepExecutionID: string,
  fromStepExecutionID: string,
  stepType: string,
  stepInput: unknown,
): Data {
  const startEvents = history
    .filter((candidate) => candidate.type === 'FlowStartedOrContinued')
    .filter((candidate) => candidate.eventId < event.eventId)
    .sort((left, right) => right.eventId - left.eventId);
  for (const startEvent of startEvents) {
    const continued = asData(startEvent.payload.continuedStart);
    if (!hasData(continued)) continue;
    const resume = asDataArray(continued.stepsToResume)
      .find((candidate) => candidate.stepExecutionId === stepExecutionID);
    const resumeOptions = asData(asData(resume?.step).stepOptions);
    if (hasData(resumeOptions)) return resumeOptions;

    const movement = selectStepMovement(
      asDataArray(continued.stepsToStart),
      stepType,
      fromStepExecutionID,
      stepInput,
    );
    const movementOptions = asData(movement.stepOptions);
    if (hasData(movementOptions)) return movementOptions;
  }
  return {};
}

function findInitialStepOptions(
  event: FlowHistoryEvent,
  history: FlowHistoryEvent[],
  stepType: string,
): Data {
  const startEvent = history
    .filter((candidate) => candidate.type === 'FlowStartedOrContinued')
    .filter((candidate) => candidate.eventId < event.eventId)
    .sort((left, right) => right.eventId - left.eventId)
    .find((candidate) => {
      const initial = asData(candidate.payload.initialStart);
      return initial.startStepType === stepType;
    });
  return asData(asData(startEvent?.payload.initialStart).stepOptions);
}

function isSourceEvent(event: FlowHistoryEvent, fromStepExecutionID: string): boolean {
  if (fromStepExecutionID.startsWith('__rpc/')) {
    return event.type === 'RpcExecutionCompleted'
      && event.payload.rpcName === fromStepExecutionID.slice('__rpc/'.length);
  }
  return asData(event.payload.context).stepExecutionId === fromStepExecutionID;
}

function sourceMovements(event: FlowHistoryEvent): Data[] {
  if (event.type === 'RpcExecutionCompleted') {
    return asDataArray(asData(event.payload.stepDecision).nextSteps);
  }
  const output = asData(event.payload.output);
  return asDataArray(asData(output.stepDecision).nextSteps);
}

function selectStepMovement(
  movements: Data[],
  stepType: string,
  fromStepExecutionID: string,
  stepInput: unknown,
): Data {
  const typeMatches = movements.filter((movement) => movement.stepType === stepType);
  const hasStampedSources = typeMatches.some((movement) => (
    isPresent(movement.fromStepExecutionIdInternalOnly)
  ));
  const candidates = hasStampedSources
    ? typeMatches.filter((movement) => (
      movement.fromStepExecutionIdInternalOnly === fromStepExecutionID
    ))
    : typeMatches;
  if (candidates.length === 1) return candidates[0];

  const inputMatches = candidates.filter((movement) => valuesEqual(movement.stepInput, stepInput));
  if (inputMatches.length === 1) return inputMatches[0];
  const remaining = inputMatches.length > 0 ? inputMatches : candidates;
  if (remaining.length === 0) return {};
  const firstOptions = remaining[0].stepOptions;
  return remaining.every((movement) => valuesEqual(movement.stepOptions, firstOptions))
    ? remaining[0]
    : {};
}

function asData(value: unknown): Data {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Data : {};
}

function asDataArray(value: unknown): Data[] {
  return Array.isArray(value) ? value.map(asData) : [];
}

function hasData(value: Data): boolean {
  return Object.keys(value).length > 0;
}

function isPresent(value: unknown): boolean {
  return value !== undefined && value !== null && value !== '';
}

function valuesEqual(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}
