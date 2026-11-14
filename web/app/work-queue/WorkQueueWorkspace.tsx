// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useRef, useState, type CSSProperties } from 'react';
import { Link, Navigate, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { ArrowUpRight, MousePointerClick, Plus, SearchX, X } from 'lucide-react';
import { SplitHandle, useCollapsibleColumn } from '../workspace/SplitHandle';
import { AGENT_INBOX_VIEW, RUN_VIEW_PARAM, runPath, sessionRunPath, workQueuePath } from '../routes';
import '../workspace/css/workspace.css';
import '../workspace/css/experimental.css';
import './workQueue.css';
import { RUN_COPY } from '../workspace/runCopy';
import { useFlowTypesDefinitions, useV2Catalog } from '../catalog/WebCatalogProvider';
import { drawableDefinitions } from '../catalog/flowTypeDefinitionCache';
import { StartFlowDialog } from '../start/StartFlowDialog';
import { SESSION_COPY } from '../agents/copy';
import { agentSessionFlowTypes, NewSessionDialog } from '../agents/NewSessionDialog';
import { RunList } from '../workspace/RunList';
import { RunSearch } from '../workspace/RunSearch';
import {
  EMPTY_RUN_QUERY,
  emptyRunsText,
  flowStatusQueryValue,
  searchedFlowTypes,
  stepTypeOptions,
} from '../workspace/runQuery';
import type { FilterRow } from '../workspace/filters';
import { SelectedRunPanel } from '../workspace/SelectedRunPanel';
import { useFlowSearch } from '../workspace/useFlowSearch';
import { useRunQuery } from '../workspace/useRunQuery';
import { useStrandedRuns } from '../workspace/useStrandedRuns';
import { useWorkingAs } from '../workspace/useWorkingAs';
import { AgentInboxWorkspace } from './AgentInboxWorkspace';
import { inboxSessionSchemas } from './agentInbox';
import { WORK_QUEUE_COPY } from './copy';
import { openFlowStatusLabel } from './liveness';
import { WorkingAsControl, WorkQueueModeSwitch } from './WorkQueueControls';

/** Its own key and a wider default than Connectors' list, because the filter chips need the room. */
const WORK_QUEUE_LIST_WIDTH_KEY = 'dex-web.work-queue-list-w';
const WORK_QUEUE_LIST_WIDTH_DEFAULT = 380;

/**
 * What has arrived for the reader. No canvas: clearing work does not need the shape of the
 * process, and "See the process" opens the same run in Run mode.
 *
 * Shares its list and its case panel with Run, so the two cannot drift apart. What differs is
 * deliberate: the scope control, and evidence before the decision.
 */
export function WorkQueueWorkspace() {
  const [searchParams] = useSearchParams();
  return searchParams.get(RUN_VIEW_PARAM) === AGENT_INBOX_VIEW ? <AgentInboxWorkspace /> : <RunQueueWorkspace />;
}

function RunQueueWorkspace() {
  const { flowType = '', flowId = '' } = useParams();
  const navigate = useNavigate();
  const {
    ready, canUseV2, catalog, catalogError, error: listError, definitionUpdateKey, handleDefinitionError, revisionFor,
  } = useV2Catalog();
  const error = listError || catalogError;
  const [startOpen, setStartOpen] = useState(false);
  const [startNotice, setStartNotice] = useState('');
  const [isNewSessionOpen, setNewSessionOpen] = useState(false);
  const entry = catalog?.flows.find((candidate) => candidate.flowType === flowType);
  // A work queue opens on active work; the control widens it.
  const runQuery = useRunQuery(
    entry?.definition,
    flowType,
    { ...EMPTY_RUN_QUERY, status: flowStatusQueryValue(openFlowStatusLabel()) },
  );
  const searchFlowTypes = searchedFlowTypes(runQuery.appliedFlowTypes, flowType);
  const searchedDefinitions = (catalog?.flows ?? [])
    .filter((candidate) => searchFlowTypes.includes(candidate.flowType))
    .map((candidate) => candidate.definition);
  const workingAs = useWorkingAs(searchedDefinitions);
  const { workQueuePermissions } = workingAs;
  const search = useFlowSearch(
    searchFlowTypes,
    entry?.definition,
    runQuery.appliedFilters,
    workQueuePermissions,
  );
  // Only the Step type filter reads graphs, and only those of the Flow types being searched.
  const stepTypeDefinitions = useFlowTypesDefinitions(searchedFlowTypes(runQuery.query.flowTypes, flowType))
    .flatMap((definitions) => drawableDefinitions(definitions));
  const { strandedFlowIDs, rememberStranded } = useStrandedRuns();
  const shellRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const listPane = useCollapsibleColumn(WORK_QUEUE_LIST_WIDTH_KEY, WORK_QUEUE_LIST_WIDTH_DEFAULT);

  useEffect(() => {
    if (definitionUpdateKey > 0) {
      setStartOpen(false);
      setNewSessionOpen(false);
      navigate(workQueuePath(flowType || undefined), { replace: true });
    }
  }, [definitionUpdateKey, flowType, navigate]);
  useEffect(() => { setStartOpen(false); setStartNotice(''); }, [flowType, entry?.definitionRevision]);

  if (!ready) return <div className="page-loading">Loading Dex Web…</div>;
  if (!canUseV2) return <Navigate to="/v1/flows" replace />;
  if (error) {
    return (
      <div className="page-shell v1-page">
        <div className="error-banner">{error}</div>
      </div>
    );
  }
  if (!catalog) return <div className="page-loading">Loading Dex Web…</div>;
  if (catalog.flows.length === 0) {
    return (
      <div className="v2-shell v2-run">
        <div className="v2-empty">Load a valid Flow Definition Graph 2.0 file to use Work Queue.</div>
      </div>
    );
  }
  if (!flowType) return <Navigate to={workQueuePath(catalog.flows[0].flowType)} replace />;
  if (!entry) return <Navigate to={workQueuePath()} replace />;

  const selectedFlow = search.flows.find((flow) => flow.flowId === flowId);
  const sessionFlowTypes = agentSessionFlowTypes(catalog);
  const isSessionFlowType = sessionFlowTypes.includes(entry.flowType);
  const paneStyle = { '--v2-list-w': `${listPane.width}px` } as CSSProperties;
  return (
    <div className="v2-shell v2-run wq" ref={shellRef} style={paneStyle}>
      <div className="v2-run-body wq-body" data-selected={flowId !== '' ? 'true' : undefined} ref={bodyRef}>
        <RunList
          collapsed={listPane.isCollapsed}
          emptyText={emptyRunsText(
            runQuery.appliedFilters,
            searchFlowTypes,
            isOpenWorkSearch(runQuery.appliedFilters) ? WORK_QUEUE_COPY.clear : WORK_QUEUE_COPY.noMatch,
          )}
          entry={entry}
          flowTypes={catalog.flows}
          heading={WORK_QUEUE_COPY.appName}
          headerNote={WORK_QUEUE_COPY.liveNote}
          onExpand={listPane.expand}
          headerAction={(
            <>
              {entry.definition.start && (
                <button className="wq-start" onClick={() => setStartOpen(true)} type="button">
                  <Plus aria-hidden="true" size={15} strokeWidth={2.4} />
                  Start Flow
                </button>
              )}
              {!entry.definition.start && isSessionFlowType && (
                <button className="wq-start" onClick={() => setNewSessionOpen(true)} type="button">
                  <Plus aria-hidden="true" size={15} strokeWidth={2.4} />
                  {SESSION_COPY.newSession}
                </button>
              )}
            </>
          )}
          modeSwitch={(
            <WorkQueueModeSwitch
              hasAgents={inboxSessionSchemas(catalog.flows).size > 0}
              mode="runs"
            />
          )}
          scope={(
            <RunSearch
              busy={search.loading}
              permissionControl={workingAs.permissionOptions.length > 0 ? (
                <WorkingAsControl definitions={searchedDefinitions} workingAs={workingAs} />
              ) : undefined}
              definition={entry.definition}
              flowType={entry.flowType}
              flowTypes={catalog.flows.map((candidate) => candidate.flowType)}
              stepTypes={stepTypeOptions(
                stepTypeDefinitions,
                searchedFlowTypes(runQuery.query.flowTypes, entry.flowType),
                runQuery.query.activeStepTypes,
              )}
              query={runQuery.query}
              onApply={runQuery.apply}
              onChange={runQuery.setQuery}
              onClear={runQuery.clear}
              onSubmit={runQuery.submit}
            />
          )}
          search={search}
          selectedFlowID={flowId}
          showFlowType={searchFlowTypes.length > 1}
          strandedFlowIDs={strandedFlowIDs}
          onSelectFlowType={(next) => {
            runQuery.narrowToPageFlowType();
            navigate(workQueuePath(next));
          }}
          onSelectRun={(flow) => navigate(workQueuePath(flow.flowType, flow.flowId))}
        />
        <SplitHandle
          axis="column"
          cssVariable="--v2-list-w"
          edge="end"
          pane="list"
          targetRef={shellRef}
          measureRef={bodyRef}
          value={listPane.width}
          ariaLabel={RUN_COPY.resizeList}
          onCommit={listPane.commit}
          onToggle={listPane.isCollapsed ? listPane.expand : listPane.collapse}
        />
        <section className="v2-work-queue-case" aria-label={flowId || WORK_QUEUE_COPY.appName}>
          {flowId ? (
            <div className="wq-case-enter" key={flowId}>
              <SelectedRunPanel
                definition={entry.definition}
                flowId={flowId}
                flowStatusCode={selectedFlow?.flowStatusCode}
                flowType={entry.flowType}
                order="evidence-first"
                onStranded={rememberStranded}
                workQueuePermissions={workQueuePermissions}
                footer={(
                  <>
                    <Link className="wq-seemore" to={runPath(flowId)}>
                      {WORK_QUEUE_COPY.seeProcess}
                      <ArrowUpRight aria-hidden="true" size={14} />
                    </Link>
                    <button
                      aria-label={WORK_QUEUE_COPY.close}
                      className="wq-close"
                      onClick={() => navigate(workQueuePath(entry.flowType))}
                      type="button"
                    >
                      <X aria-hidden="true" size={16} />
                    </button>
                  </>
                )}
              />
            </div>
          ) : (
            <WorkQueueEmptyState
              hasRuns={search.flows.length > 0}
              hasMore={search.hasNextPage}
              runCount={search.flows.length}
              settled={search.liveness === 'ok'}
            />
          )}
        </section>
      </div>
      {startNotice && <div className="sfd-notice" role="status">{startNotice}</div>}
      {isNewSessionOpen && isSessionFlowType && (
        <NewSessionDialog
          definitionRevisionFor={(sessionFlowType) => revisionFor([sessionFlowType])}
          flowTypes={sessionFlowTypes}
          initialFlowType={entry.flowType}
          onClose={() => setNewSessionOpen(false)}
          onDefinitionChanged={handleDefinitionError}
          onStarted={(_startedFlowType, sessionId) => {
            setNewSessionOpen(false);
            navigate(sessionRunPath(sessionId));
          }}
        />
      )}
      {startOpen && entry.definition.start && (
        <StartFlowDialog
          definition={entry.definition.start}
          definitionRevision={revisionFor([entry.flowType])}
          flowType={entry.flowType}
          onClose={() => setStartOpen(false)}
          onDefinitionChanged={handleDefinitionError}
          onStarted={(startedFlowID) => {
            setStartOpen(false);
            setStartNotice(`Started Flow ${startedFlowID}.`);
            void search.runSearch();
            navigate(workQueuePath(entry.flowType, startedFlowID));
          }}
        />
      )}
    </div>
  );
}

/** "Nothing open" is true only of a search for open runs. */
function isOpenWorkSearch(filters: readonly FilterRow[]): boolean {
  return filters.some((filter) => filter.field === 'executionStatus'
    && filter.value === flowStatusQueryValue(openFlowStatusLabel()));
}

/** The case panel before a run is chosen: what to do next, and how many runs are listed. */
function WorkQueueEmptyState({ hasRuns, hasMore, runCount, settled }: {
  hasRuns: boolean;
  hasMore: boolean;
  runCount: number;
  settled: boolean;
}) {
  const isClear = settled && !hasRuns;
  const Icon = isClear ? SearchX : MousePointerClick;
  return (
    <div className="wq-empty" data-kind={isClear ? 'no-runs' : 'select'}>
      <span className="wq-empty-icon"><Icon aria-hidden="true" size={22} strokeWidth={1.9} /></span>
      <h2>{isClear ? WORK_QUEUE_COPY.noRunsHeading : WORK_QUEUE_COPY.selectHeading}</h2>
      <p>{isClear ? WORK_QUEUE_COPY.noRunsPrompt : WORK_QUEUE_COPY.selectPrompt}</p>
      {hasRuns && <p className="wq-empty-count">{WORK_QUEUE_COPY.listedRuns(runCount, hasMore)}</p>}
    </div>
  );
}
