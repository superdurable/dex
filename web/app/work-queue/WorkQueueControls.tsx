// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Link } from 'react-router-dom';
import { Bot, ChevronDown, Info, ListChecks, UserRound } from 'lucide-react';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { agentInboxPath, workQueuePath } from '../routes';
import { permissionActionLabelsAcross } from '../workspace/permissions';
import type { useWorkingAs } from '../workspace/useWorkingAs';
import { AGENT_INBOX_COPY } from './agentCopy';
import { WORK_QUEUE_COPY } from './copy';

/** Runs or Agents. Shown only when some Flow type is a Dex Agents session. */
export function WorkQueueModeSwitch({ mode, hasAgents }: { mode: 'runs' | 'agents'; hasAgents: boolean }) {
  if (!hasAgents) return null;
  return (
    <nav className="wq-modes" aria-label={AGENT_INBOX_COPY.modeLabel}>
      <Link aria-current={mode === 'runs' ? 'page' : undefined} to={workQueuePath()}>
        <ListChecks aria-hidden="true" size={14} />
        {AGENT_INBOX_COPY.runsMode}
      </Link>
      <Link aria-current={mode === 'agents' ? 'page' : undefined} to={agentInboxPath()}>
        <Bot aria-hidden="true" size={14} />
        {AGENT_INBOX_COPY.agentsMode}
      </Link>
    </nav>
  );
}

/**
 * The "Working as" picker, for the permissions the searched Flow types declare.
 *
 * A chip in the filter row; what the chosen permission includes reads on its own line below it.
 */
export function WorkingAsControl({ definitions, workingAs }: {
  definitions: readonly FlowV2Definition[];
  workingAs: ReturnType<typeof useWorkingAs>;
}) {
  const { permission, permissionOptions, setPermission } = workingAs;
  if (permissionOptions.length === 0) return null;
  return (
    <>
      <label className="wq-chip wq-working-as" data-active={permission !== '' ? 'true' : undefined}>
        <UserRound aria-hidden="true" size={14} />
        <span className="wq-chip-label">{WORK_QUEUE_COPY.permissionLabel}</span>
        <select
          aria-label={WORK_QUEUE_COPY.permissionLabel}
          className="wq-chip-select"
          value={permission}
          onChange={(event) => setPermission(event.target.value)}
        >
          <option value="">{WORK_QUEUE_COPY.anyPermission}</option>
          {permissionOptions.map((candidate) => (
            <option key={candidate} value={candidate}>{candidate}</option>
          ))}
        </select>
        <ChevronDown aria-hidden="true" className="wq-chip-chevron" size={13} />
      </label>
      {permission !== '' && (
        <small className="wq-note wq-permission-note">
          <Info aria-hidden="true" size={13} />
          {WORK_QUEUE_COPY.permissionActions(permissionActionLabelsAcross(definitions, permission))}
        </small>
      )}
    </>
  );
}
