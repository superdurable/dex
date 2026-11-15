// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useId, useMemo } from 'react';
import type { TimezonePreference } from '@/lib/format';
import { historyEventTimeFromLocalInput, subFlowResetPolicyOptions, TIME_TRAVEL_STEP_METHOD } from '@/lib/timeTravel';
import type { FlowHistoryEvent } from '@/lib/types';

/** A point in a run's history, shared by Time Travel and Clone Fork; each maps it to its own type numbers. */
export type HistoryPointKind =
  | 'current'
  | 'beginning'
  | 'history-event-time'
  | 'step-type'
  | 'step-execution-id'
  | 'event-id';

/** The Step types that a run's loaded events name, for the Step type suggestions. */
export function useStepTypesOf(events: FlowHistoryEvent[]): string[] {
  return useMemo(() => {
    const values = new Set<string>();
    events.forEach((event) => {
      const context = event.payload.context as Record<string, unknown> | undefined;
      if (typeof context?.stepType === 'string') values.add(context.stepType);
    });
    return [...values];
  }, [events]);
}

/** Returns true when the point's target and Step method are filled in and valid. */
export function isHistoryPointComplete(
  kind: HistoryPointKind,
  target: string,
  stepMethod: number,
  timezone: TimezonePreference,
): boolean {
  switch (kind) {
    case 'current':
    case 'beginning':
      return true;
    case 'history-event-time':
      return historyEventTimeFromLocalInput(target, timezone) !== '';
    case 'step-type':
      return target.trim() !== '';
    case 'step-execution-id':
      return target.trim() !== '' && stepMethod !== 0;
    case 'event-id':
      return /^[1-9]\d*$/.test(target.trim()) && Number.isSafeInteger(Number(target.trim()));
  }
}

/** The request fields of a point, shared by both endpoints. */
export function historyPointRequestFields(
  kind: HistoryPointKind,
  target: string,
  stepMethod: number,
  timezone: TimezonePreference,
): Record<string, unknown> {
  switch (kind) {
    case 'history-event-time':
      return { historyEventTime: historyEventTimeFromLocalInput(target, timezone) };
    case 'step-type':
      return { stepType: target.trim() };
    case 'step-execution-id':
      return { stepExecutionId: target.trim(), stepMethod };
    case 'event-id':
      return { eventId: Number(target.trim()) };
    default:
      return {};
  }
}

/** The target and Step method inputs of a selected point. */
export function HistoryPointFields({
  kind,
  target,
  stepMethod,
  stepTypes,
  timezone,
  onTargetChange,
  onStepMethodChange,
}: {
  kind: HistoryPointKind;
  target: string;
  stepMethod: number;
  stepTypes: string[];
  timezone: TimezonePreference;
  onTargetChange: (target: string) => void;
  onStepMethodChange: (stepMethod: number) => void;
}) {
  const stepTypesListId = useId();
  if (kind === 'current' || kind === 'beginning') return null;
  return (
    <>
      <label>
        {kind === 'history-event-time' ? `Target (${timezone === 'UTC' ? 'UTC' : 'local time'})` : 'Target'}
        {kind === 'step-type' ? (
          <>
            <input list={stepTypesListId} value={target} onChange={(event) => onTargetChange(event.target.value)} />
            <datalist id={stepTypesListId}>{stepTypes.map((value) => <option value={value} key={value} />)}</datalist>
          </>
        ) : (
          <input
            type={kind === 'history-event-time' ? 'datetime-local' : 'text'}
            inputMode={kind === 'event-id' ? 'numeric' : undefined}
            placeholder={kind === 'event-id' ? 'Event number, such as 12' : undefined}
            step={kind === 'history-event-time' ? 1 : undefined}
            value={target}
            onChange={(event) => onTargetChange(event.target.value)}
          />
        )}
      </label>
      {kind === 'step-execution-id' && (
        <label>
          Step method
          <select value={stepMethod} onChange={(event) => onStepMethodChange(Number(event.target.value))}>
            <option value={0}>Select a method</option>
            <option value={TIME_TRAVEL_STEP_METHOD.WAIT_FOR}>WaitFor</option>
            <option value={TIME_TRAVEL_STEP_METHOD.EXECUTE}>Execute</option>
          </select>
        </label>
      )}
    </>
  );
}

/** Chooses what happens to SubFlow conditions that restored or copied Steps wait on. */
export function SubFlowResetPolicyField({
  value,
  onChange,
}: {
  value: number;
  onChange: (value: number) => void;
}) {
  return (
    <label>
      SubFlow reset policy
      <select value={value} onChange={(event) => onChange(Number(event.target.value))}>
        {subFlowResetPolicyOptions.map((option) => (
          <option value={option.value} key={option.value}>{option.label}</option>
        ))}
      </select>
      <small className="muted">What happens to a SubFlow that a Step at this point still waits on.</small>
    </label>
  );
}
