// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { V2CatalogEntry } from '@/lib/types';
import type { AgentSessionSchema } from '../../agents/sessionSchema';
import { SessionView } from '../../agents/SessionView';
import { WorkingAsControl } from '../../work-queue/WorkQueueControls';
import { SelectedRunPanel } from '../../workspace/SelectedRunPanel';
import { useWorkingAs } from '../../workspace/useWorkingAs';
import '../../workspace/css/workspace.css';

/** A Dex Agents session read as one conversation, with its controls. */
export function AgentSessionTab({
  entry,
  flowId,
  reloadKey,
  schema,
}: {
  entry: V2CatalogEntry;
  flowId: string;
  reloadKey: number;
  schema: AgentSessionSchema;
}) {
  const workingAs = useWorkingAs([entry.definition]);
  return (
    <section className="v2-shell sv run-experimental" aria-label="Agent session">
      <WorkingAsBar entry={entry} workingAs={workingAs} />
      <SessionView
        key={flowId}
        flowId={flowId}
        flowType={entry.flowType}
        reloadKey={reloadKey}
        schema={schema}
        workQueuePermissions={workingAs.workQueuePermissions}
        onChooseWorkingAs={workingAs.setPermission}
      />
    </section>
  );
}

/** The run's Display fields, their edits, and the Actions it offers now. */
export function RunActionsTab({
  entry,
  flowId,
  flowStatusCode,
  reloadKey,
}: {
  entry: V2CatalogEntry;
  flowId: string;
  flowStatusCode?: number;
  reloadKey: number;
}) {
  const workingAs = useWorkingAs([entry.definition]);
  const hasActions = entry.definition.actions.length > 0;
  return (
    <section className="v2-shell sv run-experimental" aria-label="Actions">
      <WorkingAsBar entry={entry} workingAs={workingAs} />
      {hasActions && workingAs.permission === '' && (
        <p className="run-experimental-hint">Choose Working as to see the Actions that permission can take.</p>
      )}
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

function WorkingAsBar({ entry, workingAs }: { entry: V2CatalogEntry; workingAs: ReturnType<typeof useWorkingAs> }) {
  if (workingAs.permissionOptions.length === 0) return null;
  return (
    <div className="run-experimental-bar">
      <WorkingAsControl definitions={[entry.definition]} workingAs={workingAs} />
    </div>
  );
}
