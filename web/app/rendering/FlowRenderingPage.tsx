// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useMemo, useState } from 'react';
import { readResponseJSON } from '@/lib/http';
import { formatDate } from '@/lib/format';
import type { FlowDefinitionCatalog, FlowDefinitionEntry, FlowDefinitionSummary } from '@/lib/types';
import { FlowDesignGraph } from '../design/FlowDesignGraph';
import { DesignUnavailable } from '../design/DesignCanvasErrorBoundary';
import { ExperimentalBadge } from '../components/ExperimentalBadge';
import { PageBreadcrumb } from '../components/PageBreadcrumb';
import { drawableDefinitions, fetchFlowTypeDefinitions } from '../catalog/flowTypeDefinitionCache';
import { useTimezonePreference } from '../providers';
import { FlowDefinitionDiagnostics } from './FlowDefinitionDiagnostics';
import { webURL } from '@/lib/basePath';

/** Where the Dex Web reference explains how definitions reach Dex Web. */
export const FLOW_DEFINITION_SOURCES_URL = 'https://docs.superdurable.io/reference/web/flow-visualization#definition-sources';

/** The selected definition's graph, read from its own Flow type route. */
type SelectedGraph =
  | { state: 'loading' }
  | { state: 'ready'; definition: FlowDefinitionEntry }
  | { state: 'failed'; message: string };

export function FlowRenderingPage() {
  const [catalog, setCatalog] = useState<FlowDefinitionCatalog | null>(null);
  const [selectedID, setSelectedID] = useState(() => new URLSearchParams(window.location.search).get('definition') ?? '');
  const [selectedGraph, setSelectedGraph] = useState<SelectedGraph>({ state: 'loading' });
  const [error, setError] = useState('');

  useEffect(() => {
    const controller = new AbortController();
    void loadCatalog(controller.signal, setCatalog, setError);
    return () => controller.abort();
  }, []);

  useEffect(() => {
    if (!catalog || catalog.definitions.length === 0) return;
    if (!catalog.definitions.some((definition) => definition.id === selectedID)) {
      setSelectedID(catalog.definitions[0].id);
    }
  }, [catalog, selectedID]);

  const selected = useMemo(
    () => catalog?.definitions.find((definition) => definition.id === selectedID),
    [catalog, selectedID],
  );

  useEffect(() => {
    if (!selected || selected.error) return undefined;
    const controller = new AbortController();
    setSelectedGraph({ state: 'loading' });
    fetchFlowTypeDefinitions(selected.flowName, controller.signal)
      .then((value) => {
        const definition = drawableDefinitions(value).find((candidate) => candidate.id === selected.id);
        setSelectedGraph(definition
          ? { state: 'ready', definition }
          : { state: 'failed', message: 'This definition changed or was removed. Refresh the page.' });
      })
      .catch((loadError: unknown) => {
        if (controller.signal.aborted) return;
        setSelectedGraph({ state: 'failed', message: loadError instanceof Error ? loadError.message : 'The definition failed to load' });
      });
    return () => controller.abort();
  }, [selected]);

  const subFlowLinks = useMemo(() => {
    const links: Record<string, string> = {};
    if (selectedGraph.state !== 'ready') return links;
    for (const node of selectedGraph.definition.graph.nodes) {
      if (node.kind !== 'subflow' || typeof node.metadata?.flowType !== 'string') continue;
      const children = catalog?.definitions.filter((definition) => (
        definition.valid && !definition.error && definition.flowName === node.metadata?.flowType
      )) ?? [];
      if (children.length === 1) links[node.id] = `?definition=${encodeURIComponent(children[0].id)}`;
    }
    return links;
  }, [catalog, selectedGraph]);

  return (
    <div className="page-shell flow-rendering-page v1-page">
      <PageBreadcrumb pageName="Designs" />
      <section className="flow-rendering-header">
        <div>
          <h1>Designs <ExperimentalBadge className="page-title-badge" /></h1>
          <p>Explore statically possible Step paths generated from Go, Python, or TypeScript source.</p>
        </div>
        {catalog?.configured && catalog.definitions.length > 0 && (
          <div className="flow-rendering-summary">
            <b>{catalog.definitions.length} definitions</b>
            {catalog.directory && <code title={catalog.directory}>{catalog.directory}</code>}
          </div>
        )}
      </section>

      {error && <div className="error-banner">{error}</div>}
      {catalog?.sourceErrors?.map((sourceError) => (
        <div className="error-banner" key={sourceError}>Some Flow definitions could not be read: {sourceError}</div>
      ))}
      {!catalog && !error && <div className="card empty-state"><h3>Loading Flow definitions…</h3></div>}
      {catalog && catalog.definitions.length === 0 && <NoFlowDefinitions catalog={catalog} />}
      {catalog?.configured && catalog.definitions.length > 0 && (
        <div className="flow-rendering-workspace">
          <aside className="flow-definition-browser card">
            <div className="flow-definition-browser-header">
              <h2>Flow definitions</h2>
              <span>{catalog.definitions.length}</span>
            </div>
            <div className="flow-definition-file-list">
              {catalog.definitions.map((definition) => (
                <DefinitionListItem
                  definition={definition}
                  isSelected={definition.id === selectedID}
                  key={definition.id}
                  onSelect={() => setSelectedID(definition.id)}
                />
              ))}
            </div>
          </aside>
          <div className="flow-rendering-graph">
            {selected?.error && (
              <DesignUnavailable title="Dex Web rejected this Flow definition" detail={selected.error} />
            )}
            {selected && !selected.error && selectedGraph.state === 'loading' && (
              <div className="dg-empty">Loading the Flow definition…</div>
            )}
            {selected && !selected.error && selectedGraph.state === 'failed' && (
              <DesignUnavailable title="The Flow definition could not be read" detail={selectedGraph.message} />
            )}
            {selected && !selected.error && selectedGraph.state === 'ready' && (
              <DefinitionCanvas
                key={selectedGraph.definition.id}
                definition={selectedGraph.definition}
                subFlowLinks={subFlowLinks}
              />
            )}
          </div>
        </div>
      )}
    </div>
  );
}

/** Explains how definitions reach Dex Web: uploaded when a Worker starts, or read from a directory. */
function NoFlowDefinitions({ catalog }: { catalog: FlowDefinitionCatalog }) {
  return (
    <div className="card empty-state flow-rendering-empty">
      <h3>No Flow definitions found</h3>
      <p>
        Generate Flow Definition Graph JSON with <strong>dexcli visualize</strong>, then give it to the Worker,
        which uploads it to the Dex Server when it starts. Dex Web reads it from there.
      </p>
      <p>
        {catalog.directory
          ? <>You can also add the JSON files to {catalog.directory} and refresh this page.</>
          : <>You can also start Dex Web with <strong>--flow-rendering-dir PATH</strong> to read JSON files from a directory.</>}
        {' '}
        <a href={FLOW_DEFINITION_SOURCES_URL} rel="noopener noreferrer" target="_blank">How Dex Web finds Flow definitions</a>
      </p>
    </div>
  );
}

function DefinitionListItem({
  definition,
  isSelected,
  onSelect,
}: {
  definition: FlowDefinitionSummary;
  isSelected: boolean;
  onSelect: () => void;
}) {
  const timezone = useTimezonePreference();
  const status = definition.error ? 'Invalid' : definition.valid ? 'Valid' : 'Diagnostics';
  return (
    <button
      aria-pressed={isSelected}
      className={isSelected ? 'is-selected' : ''}
      onClick={onSelect}
      title={definition.file || definition.flowName}
      type="button"
    >
      <span>
        <b>{definition.flowName}</b>
        <em className={definition.valid && !definition.error ? 'is-valid' : 'is-invalid'}>{status}</em>
      </span>
      {definition.file && <code>{definition.file}</code>}
      <small>{definitionSourceLabel(definition)}</small>
      {!definition.error && <small>{definition.sourceLanguage} · {definition.sourcePath}</small>}
      {definition.schemaVersion && <small>FDG {definition.schemaVersion}</small>}
      {definition.generatedAt && <small>Generated {formatDate(definition.generatedAt, timezone)}</small>}
      {definition.uploadedAt && <small>Uploaded {formatDate(definition.uploadedAt, timezone)}</small>}
    </button>
  );
}

function definitionSourceLabel(definition: FlowDefinitionSummary): string {
  switch (definition.source) {
    case 'uploaded':
      return 'Uploaded to the Dex Server';
    case 'visualize':
      return 'Generated by dexcli visualize';
    default:
      return 'Directory file';
  }
}

/** One definition drawn as the Flow design graph, then its diagnostics and child Flow links. */
function DefinitionCanvas({
  definition,
  subFlowLinks,
}: {
  definition: FlowDefinitionEntry;
  subFlowLinks: Record<string, string>;
}) {
  const children = definition.graph.nodes.filter((node) => subFlowLinks[node.id] !== undefined);
  return (
    <>
      <div className="flow-rendering-canvas">
        <FlowDesignGraph definition={definition} />
      </div>
      {children.length > 0 && (
        <p className="flow-rendering-children">
          Starts{' '}
          {children.map((node, index) => (
            <span key={node.id}>
              {index > 0 ? ', ' : ''}
              <a href={subFlowLinks[node.id]}>{node.name}</a>
            </span>
          ))}
        </p>
      )}
      <FlowDefinitionDiagnostics definition={definition} />
    </>
  );
}

async function loadCatalog(
  signal: AbortSignal,
  setCatalog: (catalog: FlowDefinitionCatalog) => void,
  setError: (message: string) => void,
): Promise<void> {
  try {
    const response = await fetch(webURL('/api/flow-definitions'), { signal });
    setCatalog(await readResponseJSON<FlowDefinitionCatalog>(response));
  } catch (loadError) {
    if (signal.aborted) return;
    setError(loadError instanceof Error ? loadError.message : 'Flow definitions failed to load');
  }
}
