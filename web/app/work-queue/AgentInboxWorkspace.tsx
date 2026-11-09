// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, Navigate, useNavigate, useParams } from 'react-router-dom';
import { agentInboxPath, sessionRunPath } from '../routes';
import '../workspace/css/workspace.css';
import { useWebCatalog } from '../catalog/WebCatalogProvider';
import { ACTIVE_STEP_TYPES_FIELD, newFilterRow, type FilterRow } from '../workspace/filters';
import { flowStatusQueryValue } from '../workspace/runQuery';
import { useFlowSearch } from '../workspace/useFlowSearch';
import { useWorkingAs } from '../workspace/useWorkingAs';
import { SESSION_COPY } from '../agents/copy';
import { AGENT_INBOX_COPY } from './agentCopy';
import {
  AGENT_WAITING_STEP_TYPES,
  askedText,
  firstInterrupt,
  groupInboxRows,
  inboxSessionSchemas,
  readInboxSession,
  waitText,
  type AgentInboxRow,
  type AgentSessionRead,
} from './agentInbox';
import { AgentSessionPanel } from './AgentSessionPanel';
import { openFlowStatusLabel, readFailureReason } from './liveness';
import { WORK_QUEUE_COPY } from './copy';
import { WorkingAsControl, WorkQueueModeSwitch } from './WorkQueueControls';

const CLOCK_INTERVAL_MS = 30_000;
const READ_CONCURRENCY = 6;

interface HeldRead {
  read: AgentSessionRead | null;
  error: string;
}

/**
 * The agent mode of Work Queue: Dex Agents sessions that wait for a person, grouped by agent.
 *
 * Discovery is the built-in ActiveStepTypes search across every agent Flow type. What each row
 * asks, and its deadline, is read from the session afterwards, so ordering covers one page.
 */
export function AgentInboxWorkspace() {
  const { flowType = '', flowId = '' } = useParams();
  const navigate = useNavigate();
  const { ready, canUseV2, catalog, definitions, error } = useWebCatalog();
  const schemas = useMemo(
    () => inboxSessionSchemas(catalog?.flows ?? [], definitions?.definitions ?? []),
    [catalog, definitions],
  );
  const agentFlowTypes = useMemo(() => [...schemas.keys()], [schemas]);
  const agentDefinitions = useMemo(() => [...schemas.values()].map((entry) => entry.definition), [schemas]);
  const workingAs = useWorkingAs(agentDefinitions);
  const filters = useMemo<FilterRow[]>(() => [
    { ...newFilterRow(ACTIVE_STEP_TYPES_FIELD, 'in', AGENT_WAITING_STEP_TYPES.join(', ')), values: AGENT_WAITING_STEP_TYPES },
    newFilterRow('executionStatus', 'eq', flowStatusQueryValue(openFlowStatusLabel())),
  ], []);
  const search = useFlowSearch(agentFlowTypes, agentDefinitions[0], filters, workingAs.workQueuePermissions);
  const reads = useSessionReads(search.flows.map((flow) => flow.flowId));
  const nowMs = useClock();

  const { runSearch } = search;
  const isFirstPermission = useRef(true);
  useEffect(() => {
    if (isFirstPermission.current) {
      isFirstPermission.current = false;
      return;
    }
    runSearch();
  }, [runSearch, workingAs.workQueuePermissions]);

  if (!ready) return <div className="page-loading">Loading Dex Web…</div>;
  if (!canUseV2) return <Navigate to="/v1/flows" replace />;
  if (error) return <div className="page-shell"><div className="error-banner">{error}</div></div>;
  if (!catalog) return <div className="page-loading">Loading Dex Web…</div>;

  const rows: AgentInboxRow[] = search.flows.map((flow) => ({
    flow,
    read: reads.held.get(flow.flowId)?.read ?? null,
    readError: reads.held.get(flow.flowId)?.error ?? '',
  }));
  const groups = groupInboxRows(rows);
  const waitingCount = groups.reduce((total, group) => total + group.rows.length, 0);
  const selectedSchema = schemas.get(flowType)?.schema;
  const listState = search.liveness === 'loading'
    ? WORK_QUEUE_COPY.loading
    : search.liveness === 'unreachable'
      ? WORK_QUEUE_COPY.unreachable
      : search.liveness === 'stale'
        ? WORK_QUEUE_COPY.stale
        : '';

  return (
    <div className="v2-shell v2-run">
      <div className="ai-body" data-selected={flowId !== '' ? 'true' : undefined}>
        <aside className="rsw ai-list" aria-label={AGENT_INBOX_COPY.heading}>
          <div className="sq-head">
            <span className="sq-title">{WORK_QUEUE_COPY.appName}</span>
            <WorkQueueModeSwitch hasAgents mode="agents" />
          </div>
          <section className="rsw-zone" data-zone="permission">
            <WorkingAsControl definitions={agentDefinitions} workingAs={workingAs} />
          </section>
          <div className="ai-summary">
            <b>{AGENT_INBOX_COPY.heading}</b>
            <span>{AGENT_INBOX_COPY.waiting(waitingCount)}</span>
          </div>
          {listState !== '' && <p className="sq-state" data-liveness={search.liveness}>{listState}</p>}
          {search.liveness === 'ok' && waitingCount === 0 && (
            <>
              <p className="sq-state">{AGENT_INBOX_COPY.empty}</p>
              <p className="sc-why">{AGENT_INBOX_COPY.emptyWhy}</p>
            </>
          )}
          {search.hasNextPage && <p className="sq-state">{AGENT_INBOX_COPY.pageOnly(search.flows.length)}</p>}
          <div className="rsw-scroll" data-zone="list">
            {groups.map((group) => (
              <section className="ai-group" data-group={group.flowType} key={group.flowType}>
                <h3 className="ai-grouphead">
                  <span>{group.flowType}</span>
                  <span className="ai-count" aria-label={AGENT_INBOX_COPY.waiting(group.rows.length)}>{group.rows.length}</span>
                </h3>
                <ul className="rsw-list">
                  {group.rows.map((row) => (
                    <li
                      className="rsw-item"
                      data-selected={row.flow.flowId === flowId ? 'true' : undefined}
                      key={row.flow.flowId}
                    >
                      <InboxRowButton
                        nowMs={nowMs}
                        row={row}
                        onSelect={() => navigate(agentInboxPath(row.flow.flowType, row.flow.flowId))}
                      />
                    </li>
                  ))}
                </ul>
              </section>
            ))}
          </div>
        </aside>
        <section className="v2-work-queue-case ai-case" aria-label={flowId || AGENT_INBOX_COPY.heading}>
          {flowId !== '' && selectedSchema ? (
            <AgentSessionPanel
              flowId={flowId}
              flowType={flowType}
              key={`${flowType}/${flowId}`}
              schema={selectedSchema}
              workQueuePermissions={workingAs.workQueuePermissions}
              onSettled={() => {
                reads.reread();
                runSearch();
              }}
              footer={(
                <>
                  <Link className="v2-seemore" to={sessionRunPath(flowId)}>{AGENT_INBOX_COPY.openSession}</Link>
                  <button
                    aria-label={AGENT_INBOX_COPY.close}
                    className="rhd-close"
                    onClick={() => navigate(agentInboxPath())}
                    type="button"
                  >
                    ✕
                  </button>
                </>
              )}
            />
          ) : (
            <p className="sc-none v2-work-queue-empty">{AGENT_INBOX_COPY.selectPrompt}</p>
          )}
        </section>
      </div>
    </div>
  );
}

function InboxRowButton({ row, nowMs, onSelect }: { row: AgentInboxRow; nowMs: number; onSelect: () => void }) {
  const first = row.read ? firstInterrupt(row.read.interrupts) : null;
  const extra = (row.read?.interrupts.length ?? 0) - 1;
  return (
    <button className="rsw-row ai-row" onClick={onSelect} type="button">
      <span className="rsw-id ai-asked" title={first ? askedText(first) : row.flow.flowId}>
        {first ? askedText(first) : row.readError !== '' ? row.flow.flowId : SESSION_COPY.loading}
      </span>
      {first && (
        <span className="ai-chip">
          {first.kind === 'question' ? AGENT_INBOX_COPY.question : AGENT_INBOX_COPY.approval}
        </span>
      )}
      <span className="rsw-state">
        <span className="t-mono">{row.flow.flowId}</span>
        {first && <> · {waitText(first, nowMs)}</>}
        {extra > 0 && <> · {AGENT_INBOX_COPY.more(extra)}</>}
      </span>
      {row.readError !== '' && <span className="rsw-stranded">{row.readError}</span>}
    </button>
  );
}

/** Reads each listed session once per search answer, a few at a time, keeping the last good read. */
function useSessionReads(flowIDs: readonly string[]) {
  const [held, setHeld] = useState<Map<string, HeldRead>>(() => new Map());
  const [rereadKey, setRereadKey] = useState(0);
  const key = flowIDs.join('\n');

  useEffect(() => {
    const controller = new AbortController();
    const queue = [...flowIDs];
    const worker = async () => {
      for (let next = queue.shift(); next !== undefined; next = queue.shift()) {
        const flowID = next;
        try {
          const read = await readInboxSession(flowID, controller.signal);
          setHeld((current) => new Map(current).set(flowID, { read, error: '' }));
        } catch (readError) {
          if (controller.signal.aborted) return;
          setHeld((current) => new Map(current).set(flowID, {
            read: current.get(flowID)?.read ?? null,
            error: readFailureReason(readError),
          }));
        }
      }
    };
    for (let index = 0; index < READ_CONCURRENCY; index += 1) void worker();
    return () => controller.abort();
  }, [key, rereadKey]);

  /** After an answer the search index lags, so the rows are read again from committed state. */
  const reread = useCallback(() => setRereadKey((current) => current + 1), []);
  return { held, reread };
}

function useClock(): number {
  const [nowMs, setNowMs] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNowMs(Date.now()), CLOCK_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, []);
  return nowMs;
}
