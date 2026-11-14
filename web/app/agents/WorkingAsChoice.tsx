// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createContext, useContext, useId, useState } from 'react';
import { ShieldCheck } from 'lucide-react';
import { SESSION_COPY } from './copy';

/** The Conversation tab's Working as: the current permission, its choices, and how to set it. */
export interface WorkingAsChoice {
  /** The permission Working as holds, or '' for Anyone. */
  permission: string;
  /** Every permission the session's Flow type declares. */
  options: readonly string[];
  choose: (permission: string) => void;
}

const WorkingAsChoiceContext = createContext<WorkingAsChoice | null>(null);

/** Lets every control in the tab offer Work as where Working as blocks it. */
export const WorkingAsChoiceProvider = WorkingAsChoiceContext.Provider;

/** The tab's Working as, or null outside the Conversation tab, such as in Work Queue. */
export function useWorkingAsChoice(): WorkingAsChoice | null {
  return useContext(WorkingAsChoiceContext);
}

/** A blocked control's reason; when only Working as blocks it, a Work as button takes the reason's place. */
export function BlockedReason({ reason, label }: { reason: string; label?: string }) {
  const choice = useWorkingAsChoice();
  const permission = choice?.options.find((option) => reason === SESSION_COPY.chooseWorkingAs(option));
  if (!choice || !permission) return <p className="agc-blocked">{label ? `${label}: ${reason}` : reason}</p>;
  return (
    <p className="agc-blocked ses-workas-line">
      {label && <span>{label}:</span>}
      <button className="ses-workas" onClick={() => choice.choose(permission)} type="button">
        <ShieldCheck aria-hidden="true" size={13} strokeWidth={2.2} />
        {SESSION_COPY.workAs(permission)}
      </button>
      <span>{SESSION_COPY.workAsToAct}</span>
    </p>
  );
}

/** One muted line once Working as is set: whom the tab acts as, and Change for the other choices. */
export function ActingAsLine() {
  const choice = useWorkingAsChoice();
  const [isChanging, setIsChanging] = useState(false);
  const selectId = useId();
  if (!choice || choice.permission === '') return null;
  return (
    <p className="agc-note ses-acting-as">
      {isChanging ? (
        <>
          <label htmlFor={selectId}>{SESSION_COPY.workingAsLabel}</label>
          <select
            autoFocus
            id={selectId}
            value={choice.permission}
            onBlur={() => setIsChanging(false)}
            onChange={(event) => {
              choice.choose(event.target.value);
              setIsChanging(false);
            }}
          >
            <option value="">{SESSION_COPY.workingAsAnyone}</option>
            {choice.options.map((option) => <option key={option} value={option}>{option}</option>)}
          </select>
        </>
      ) : (
        <>
          <span>{SESSION_COPY.actingAs(choice.permission)}</span>
          <span aria-hidden="true"> · </span>
          <button
            aria-label={SESSION_COPY.changeWorkingAs}
            className="ses-acting-as-change"
            onClick={() => setIsChanging(true)}
            type="button"
          >
            Change
          </button>
        </>
      )}
    </p>
  );
}
