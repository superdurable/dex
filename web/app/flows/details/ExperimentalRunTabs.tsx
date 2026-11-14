// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import type { V2CatalogEntry } from '@/lib/types';
import { SESSION_COPY } from '../../agents/copy';
import { agentSessionFlowTypes, NewSessionDialog } from '../../agents/NewSessionDialog';
import type { AgentSessionSchema } from '../../agents/sessionSchema';
import { SessionView } from '../../agents/SessionView';
import { useV2Catalog } from '../../catalog/WebCatalogProvider';
import { sessionRunPath } from '../../routes';
import { ChevronDown, Plus, UserRound } from 'lucide-react';
import { SelectedRunPanel } from '../../workspace/SelectedRunPanel';
import type { useWorkingAs } from '../../workspace/useWorkingAs';
import '../../workspace/css/workspace.css';
import '../../workspace/css/experimental.css';

/** The run page's one Working as choice, shared by Agent session and Actions. */
export type RunWorkingAs = ReturnType<typeof useWorkingAs>;

/** A Dex Agents session read as one conversation, with its controls. */
export function AgentSessionTab({
  entry,
  flowId,
  reloadKey,
  schema,
  workingAs,
}: {
  entry: V2CatalogEntry;
  flowId: string;
  reloadKey: number;
  schema: AgentSessionSchema;
  workingAs: RunWorkingAs;
}) {
  const navigate = useNavigate();
  const [isNewSessionOpen, setNewSessionOpen] = useState(false);
  return (
    <section className="v2-shell sv run-experimental xp" aria-label="Agent session">
      <header className="xp-head">
        <h3>Agent session</h3>
        <div className="xp-head-tools">
          <button className="xp-new-session" onClick={() => setNewSessionOpen(true)} type="button">
            <Plus aria-hidden="true" size={14} strokeWidth={2.2} />
            {SESSION_COPY.newSession}
          </button>
        </div>
      </header>
      {isNewSessionOpen && (
        <CatalogNewSessionDialog
          entry={entry}
          onClose={() => setNewSessionOpen(false)}
          onStarted={(sessionId) => {
            setNewSessionOpen(false);
            navigate(sessionRunPath(sessionId));
          }}
        />
      )}
      <SessionView
        key={flowId}
        flowId={flowId}
        flowType={entry.flowType}
        reloadKey={reloadKey}
        schema={schema}
        workQueuePermissions={workingAs.workQueuePermissions}
        onChooseWorkingAs={workingAs.setPermission}
        workingAsOptions={workingAs.permissionOptions}
        workingAsPermission={workingAs.permission}
      />
    </section>
  );
}

/**
 * New session for a session run. It lists every session Flow type, so it reads the whole catalog,
 * but only once the reader opens it; until then it offers the run's own Flow type.
 */
function CatalogNewSessionDialog({
  entry,
  onClose,
  onStarted,
}: {
  entry: V2CatalogEntry;
  onClose: () => void;
  onStarted: (sessionId: string) => void;
}) {
  const { catalog, handleDefinitionError, revisionFor } = useV2Catalog();
  const catalogFlowTypes = agentSessionFlowTypes(catalog);
  const flowTypes = catalogFlowTypes.includes(entry.flowType) ? catalogFlowTypes : [entry.flowType, ...catalogFlowTypes];
  return (
    <NewSessionDialog
      definitionRevisionFor={(flowType) => revisionFor([flowType])}
      flowTypes={flowTypes}
      initialFlowType={entry.flowType}
      onClose={onClose}
      onDefinitionChanged={handleDefinitionError}
      onStarted={(_flowType, sessionId) => onStarted(sessionId)}
    />
  );
}

/** The run's Display fields, their edits, and the Actions it offers now. */
export function RunActionsTab({
  entry,
  flowId,
  flowStatusCode,
  reloadKey,
  workingAs,
}: {
  entry: V2CatalogEntry;
  flowId: string;
  flowStatusCode?: number;
  reloadKey: number;
  workingAs: RunWorkingAs;
}) {
  const hasActions = entry.definition.actions.length > 0;
  return (
    <section className="v2-shell sv run-experimental xp" aria-label="Actions">
      <header className="xp-head">
        <h3>{hasActions ? 'Actions and details' : 'Details'}</h3>
        <WorkingAsBar entry={entry} workingAs={workingAs} />
      </header>
      <SelectedRunPanel
        definition={entry.definition}
        flowId={flowId}
        flowStatusCode={flowStatusCode}
        flowType={entry.flowType}
        order="actions-first"
        reloadKey={reloadKey}
        showHeading={false}
        workQueuePermissions={workingAs.workQueuePermissions}
      />
    </section>
  );
}

/** Working as, as a compact pill beside the tab's heading. */
function WorkingAsBar({ entry, workingAs }: { entry: V2CatalogEntry; workingAs: RunWorkingAs }) {
  if (workingAs.permissionOptions.length === 0) return null;
  return (
    <label className="xp-working-as" title={entry.flowType}>
      <UserRound aria-hidden="true" size={14} strokeWidth={2} />
      <span>Working as</span>
      <select
        aria-label="Working as"
        value={workingAs.permission}
        onChange={(event) => workingAs.setPermission(event.target.value)}
      >
        <option value="">Anyone</option>
        {workingAs.permissionOptions.map((permission) => (
          <option key={permission} value={permission}>{permission}</option>
        ))}
      </select>
      <ChevronDown aria-hidden="true" size={13} strokeWidth={2} />
    </label>
  );
}
