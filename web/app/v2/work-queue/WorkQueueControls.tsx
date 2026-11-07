// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Link } from 'react-router-dom';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { v2AgentInboxPath, v2WorkQueuePath } from '../contract';
import { permissionActionLabelsAcross } from '../workspace/permissions';
import type { useWorkingAs } from '../workspace/useWorkingAs';
import { AGENT_INBOX_COPY } from './agentCopy';
import { WORK_QUEUE_COPY } from './copy';

/** Runs or Agents. Shown only when some Flow type is a Dex Agents session. */
export function WorkQueueModeSwitch({ mode, hasAgents }: { mode: 'runs' | 'agents'; hasAgents: boolean }) {
  if (!hasAgents) return null;
  return (
    <nav className="ai-modes" aria-label={AGENT_INBOX_COPY.modeLabel}>
      <Link aria-current={mode === 'runs' ? 'page' : undefined} to={v2WorkQueuePath()}>{AGENT_INBOX_COPY.runsMode}</Link>
      <Link aria-current={mode === 'agents' ? 'page' : undefined} to={v2AgentInboxPath()}>{AGENT_INBOX_COPY.agentsMode}</Link>
    </nav>
  );
}

/** The "Working as" picker, for the permissions the searched Flow types declare. */
export function WorkingAsControl({ definitions, workingAs }: {
  definitions: readonly FlowV2Definition[];
  workingAs: ReturnType<typeof useWorkingAs>;
}) {
  const { permission, permissionOptions, setPermission } = workingAs;
  if (permissionOptions.length === 0) return null;
  return (
    <label className="rsw-permission">
      <span className="rsw-zonehead">{WORK_QUEUE_COPY.permissionLabel}</span>
      <select
        aria-label={WORK_QUEUE_COPY.permissionLabel}
        value={permission}
        onChange={(event) => setPermission(event.target.value)}
      >
        <option value="">{WORK_QUEUE_COPY.anyPermission}</option>
        {permissionOptions.map((candidate) => (
          <option key={candidate} value={candidate}>{candidate}</option>
        ))}
      </select>
      {permission !== '' && (
        <small className="rsw-permission-note">
          {WORK_QUEUE_COPY.permissionActions(permissionActionLabelsAcross(definitions, permission))}
        </small>
      )}
    </label>
  );
}
