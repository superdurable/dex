// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useMemo, useState } from 'react';
import { readResponseJSON } from '@/lib/http';
import type { FlowDefinitionCatalog } from '@/lib/types';
import { safeDecode } from './canvas/model/decode';
import { ArrowDefs } from './canvas/render/ArrowDefs';
import { Stage } from './canvas/render/Stage';
import { viewById } from './canvas/views';
import type { Detail, Direction } from './canvas/views/types';
import { Controls } from './flow/Controls';
import { Legend } from './flow/Legend';
import { groupsFromDefinition } from './groupsFromGraph';

export function V2Canvas({ flowType }: { flowType: string }) {
  const [catalog, setCatalog] = useState<FlowDefinitionCatalog | null>(null);
  const [detail, setDetail] = useState<Detail>('collapsed');
  const [direction, setDirection] = useState<Direction>('tb');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [selectedGroupId, setSelectedGroupId] = useState<string | null>(null);
  const [legendOpen, setLegendOpen] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    const controller = new AbortController();
    void fetch('/api/flow-definitions', { signal: controller.signal })
      .then((response) => readResponseJSON<FlowDefinitionCatalog>(response))
      .then(setCatalog)
      .catch((loadError: unknown) => {
        if (!controller.signal.aborted) {
          setError(loadError instanceof Error ? loadError.message : 'Flow definition failed to load');
        }
      });
    return () => controller.abort();
  }, []);

  const selected = useMemo(
    () => catalog?.definitions.find((definition) => definition.flowName === flowType && definition.valid),
    [catalog, flowType],
  );

  const flow = useMemo(() => {
    if (!selected) return null;
    return safeDecode(selected.graph, {
      generated: true,
      note: selected.file,
    });
  }, [selected]);

  const groups = useMemo(() => {
    if (!selected || !flow) return [];
    return groupsFromDefinition(selected.graph, flow);
  }, [flow, selected]);

  const scene = useMemo(() => {
    if (!flow) return null;
    return viewById('control').layout(flow, {
      detail,
      direction,
      selectedId,
      run: null,
      groups,
    });
  }, [detail, direction, flow, groups, selectedId]);

  if (error) return <div className="v2-empty">{error}</div>;
  if (!catalog) return <div className="v2-empty">Loading Flow definition…</div>;
  if (!selected || !flow || !scene) {
    return <div className="v2-empty">No valid Flow Definition Graph 2.0 file for this Flow type.</div>;
  }

  return (
    <div className="pcanvas">
      <ArrowDefs />
      <div className="pctlbar">
        <Controls detail={detail} direction={direction} onDetail={setDetail} onDirection={setDirection} />
      </div>
      <Stage
        scene={scene}
        detail={detail}
        direction={direction}
        selectedId={selectedId}
        selectedGroupId={selectedGroupId}
        onSelectGroup={(id) => {
          setSelectedGroupId(id);
          setSelectedId(null);
        }}
        onSelect={(id) => {
          setSelectedId(id);
          setSelectedGroupId(null);
        }}
        legend={() => (
          <div className="plegend-card" data-open={legendOpen ? 'true' : undefined}>
            <button
              type="button"
              className="plegend-toggle"
              onClick={() => setLegendOpen((open) => !open)}
              aria-expanded={legendOpen}
            >
              Legend {legendOpen ? '▾' : '▸'}
            </button>
            {legendOpen ? (
              <>
                <Legend scene={scene} flow={flow} />
                {scene.notes.length > 0 ? (
                  <div className="plegend-notes">
                    {scene.notes.map((note) => <p key={note}>{note}</p>)}
                  </div>
                ) : null}
              </>
            ) : null}
          </div>
        )}
        fitKey={`${flowType}|${detail}|${direction}|${selected.file}`}
      />
    </div>
  );
}
