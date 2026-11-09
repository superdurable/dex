// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useMemo, useState, type JSX } from 'react';
import { Link } from 'react-router-dom';
import {
  ArrowDown,
  ArrowRight,
  BookOpen,
  ChevronsDownUp,
  ChevronsUpDown,
  Plug,
  X,
} from 'lucide-react';
import type { FlowDefinitionEntry } from '@/lib/types';
import { CONNECTORS_PATH } from '../routes';
import { safeDecode } from './canvas/model/decode';
import type { Branch, PocFlow, StepModel } from './canvas/model/pocFlow';
import { ArrowDefs } from './canvas/render/ArrowDefs';
import { TileView } from './canvas/render/CardParts';
import { Stage } from './canvas/render/Stage';
import { useStageScene } from './canvas/useStageScene';
import { detourMessage, hasDetour, isFailureHandling, withDetours } from './canvas/views/failureDetours';
import { stageId } from './canvas/views/stageCard';
import { stepTitle, tileOf } from './canvas/views/stepBox';
import type { Direction } from './canvas/views/types';
import { Legend } from './flow/Legend';
import { groupsFromDefinition } from './groupsFromGraph';
import { stepContext } from './stepContext';
import './design.css';

/**
 * Draws one Flow Definition Graph: the Flow's Steps, stages and transitions, and nothing about any run.
 *
 * It reads only the definition it is given. It never loads history or stored values, so it explains
 * how a Flow is built rather than what one run did.
 */
export function FlowDesignGraph({ definition }: { definition: FlowDefinitionEntry }) {
  const [direction, setDirection] = useState<Direction>('tb');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [selectedGroupId, setSelectedGroupId] = useState<string | null>(null);
  const [detourFor, setDetourFor] = useState<string | null>(null);
  const [legendOpen, setLegendOpen] = useState(false);

  const flow = useMemo(
    () => safeDecode(definition.graph, { generated: true, note: definition.file }),
    [definition],
  );
  const groups = useMemo(() => groupsFromDefinition(definition.graph, flow), [definition, flow]);
  const stages = useStageScene({ flow, groups, direction, run: null });
  const scene = useMemo(() => {
    if (stages.scene === null) return null;
    const sources = detourFor ? [detourFor] : [];
    return withDetours(stages.scene, flow, { direction, run: null, groups, openStages: stages.openStages }, sources);
  }, [detourFor, direction, flow, groups, stages.openStages, stages.scene]);

  const { collapseStage, openStage: openStageInPlace } = stages;
  const closeStage = useCallback((groupId: string) => {
    collapseStage(groupId);
    setSelectedGroupId(null);
  }, [collapseStage]);
  const openStage = useCallback((boxId: string) => {
    openStageInPlace(boxId);
    setSelectedGroupId(groups.find((group) => stageId(group.id) === boxId)?.id ?? null);
    setSelectedId(null);
  }, [groups, openStageInPlace]);

  const selectedStep = flow.steps.find((step) => step.id === selectedId) ?? null;

  if (flow.steps.length === 0 && flow.entries.length === 0) {
    return <div className="dg-empty">This Flow Definition Graph has no Steps to draw.</div>;
  }
  if (!scene) return <div className="dg-empty">Drawing the Flow…</div>;

  return (
    <div className="dg" data-panel={selectedStep ? 'true' : undefined}>
      <div className="pcanvas dg-canvas">
        <ArrowDefs />
        <DesignToolbar
          stages={stages.hasStages ? stages.state : null}
          direction={direction}
          onCollapseAll={stages.collapseAll}
          onExpandAll={stages.expandAll}
          onDirection={setDirection}
        />
        <Stage
          key={definition.file}
          isSettled={stages.isSettled}
          dimUnrelated={selectedId !== null}
          scene={scene}
          direction={direction}
          onOpenStage={openStage}
          onCloseStage={closeStage}
          cue={stages.cue}
          selectedId={selectedId}
          selectedGroupId={selectedGroupId}
          onSelectGroup={(id) => {
            setSelectedGroupId(id);
            setSelectedId(null);
          }}
          onSelect={(id) => {
            setSelectedId(id);
            // Selecting a Step that can fail draws its detour; selecting inside the detour keeps it.
            if (id === null || !isFailureHandling(flow, id)) setDetourFor(id !== null && hasDetour(flow, id) ? id : null);
            setSelectedGroupId(null);
          }}
          legend={() => (
            <div className="plegend-card dg-legend" data-open={legendOpen ? 'true' : undefined}>
              <button
                type="button"
                className="plegend-toggle"
                onClick={() => setLegendOpen((open) => !open)}
                aria-expanded={legendOpen}
              >
                <BookOpen aria-hidden="true" size={14} strokeWidth={2} />
                Legend
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
          fitKey={`${definition.file}|${stages.view}`}
        />
        <p className="sr-only" aria-live="polite">{stages.cue?.message}</p>
        <p className="sr-only" aria-live="polite">{detourFor ? detourMessage(flow, detourFor) : ''}</p>
      </div>
      {selectedStep ? (
        <DesignStepPanel
          flow={flow}
          step={selectedStep}
          groups={groups}
          onClose={() => {
            setSelectedId(null);
            setDetourFor(null);
          }}
        />
      ) : null}
    </div>
  );
}

const DIRECTION_OPTIONS: { id: Direction; label: string; Icon: typeof ArrowDown }[] = [
  { id: 'tb', label: 'Top to bottom', Icon: ArrowDown },
  { id: 'lr', label: 'Left to right', Icon: ArrowRight },
];

/** View and Direction controls as two segmented switches. */
function DesignToolbar({
  stages,
  direction,
  onCollapseAll,
  onExpandAll,
  onDirection,
}: {
  /** Null for a Flow without stages, which has nothing to open or close. */
  stages: 'collapsed' | 'expanded' | 'mixed' | null;
  direction: Direction;
  onCollapseAll: () => void;
  onExpandAll: () => void;
  onDirection: (direction: Direction) => void;
}): JSX.Element {
  return (
    <div className="dg-toolbar">
      {stages === null ? null : (
        <div className="dg-segment" role="group" aria-label="View">
          <span className="dg-segment-label">View</span>
          <button
            type="button"
            aria-pressed={stages === 'collapsed'}
            title="Close every stage and return to the opening view"
            onClick={onCollapseAll}
          >
            <ChevronsDownUp aria-hidden="true" size={14} strokeWidth={2} />
            Collapsed
          </button>
          <button
            type="button"
            aria-pressed={stages === 'expanded'}
            title="Open every stage to show all of its Steps"
            onClick={onExpandAll}
          >
            <ChevronsUpDown aria-hidden="true" size={14} strokeWidth={2} />
            Expanded
          </button>
        </div>
      )}
      <div className="dg-segment" role="radiogroup" aria-label="Direction">
        <span className="dg-segment-label">Direction</span>
        {DIRECTION_OPTIONS.map(({ id, label, Icon }) => (
          <button
            key={id}
            type="button"
            role="radio"
            aria-checked={direction === id}
            aria-label={label}
            title={label}
            onClick={() => onDirection(id)}
          >
            <Icon aria-hidden="true" size={14} strokeWidth={2} />
          </button>
        ))}
      </div>
    </div>
  );
}

/** What one Step is for and where it leads, from the definition alone. */
function DesignStepPanel({
  flow,
  step,
  groups,
  onClose,
}: {
  flow: PocFlow;
  step: StepModel;
  groups: Parameters<typeof stepContext>[2];
  onClose: () => void;
}): JSX.Element {
  const context = stepContext(flow, step, groups);
  const paths = step.execute.branches.map((branch) => pathOf(flow, branch));
  return (
    <aside className="dg-panel" aria-label={`Step ${step.stepType}`}>
      <header className="dg-panel-head">
        <TileView tile={tileOf(flow, step)} />
        <div className="dg-panel-title">
          <h3>{stepTitle(step)}</h3>
          <code>{step.stepType}</code>
        </div>
        <button aria-label="Close the Step details" className="dg-panel-close" onClick={onClose} type="button">
          <X aria-hidden="true" size={16} strokeWidth={2} />
        </button>
      </header>
      <p className={context.explanation ? 'dg-panel-purpose' : 'dg-panel-purpose is-missing'}>
        {context.explanation ?? 'The Flow declares no explanation for this Step.'}
      </p>
      <dl className="dg-panel-facts">
        {context.facts.map((fact) => (
          <div key={fact.label}>
            <dt>{fact.label}</dt>
            <dd>{fact.value}</dd>
          </div>
        ))}
      </dl>
      {paths.length > 0 ? (
        <section className="dg-panel-paths">
          <h4>Paths out</h4>
          <ol>
            {paths.map((path, index) => (
              <li key={index} data-tone={path.tone}>
                <span className="dg-path-when">{path.when}</span>
                <span className="dg-path-then">{path.then}</span>
              </li>
            ))}
          </ol>
        </section>
      ) : null}
      {context.connector ? (
        <section className="dg-panel-connector">
          <Plug aria-hidden="true" size={16} strokeWidth={2} />
          <div>
            <b>{context.connector.connectorId}</b>
            <span>{context.connector.operationId} · {context.connector.operationKind}</span>
            <span>Connection: {context.connector.connectionName || 'Unnamed'}</span>
            <Link to={CONNECTORS_PATH}>Configure connection</Link>
          </div>
        </section>
      ) : null}
    </aside>
  );
}

interface DesignPath {
  when: string;
  then: string;
  tone: 'go' | 'end' | 'fail';
}

const CLOSE_WORDS: Record<NonNullable<Branch['closes']>, Omit<DesignPath, 'when'>> = {
  complete: { then: 'The Flow completes', tone: 'end' },
  completeIfChannelsEmpty: { then: 'The Flow completes once no messages wait', tone: 'end' },
  deadEnd: { then: 'This path ends; other paths go on', tone: 'end' },
  fail: { then: 'The Flow fails', tone: 'fail' },
};

/** One Execute branch in plain words: when it applies, and what happens next. */
function pathOf(flow: PocFlow, branch: Branch): DesignPath {
  const when = branch.guard === null ? 'Always' : branch.guard === 'otherwise' ? 'Otherwise' : `When ${branch.guard}`;
  if (branch.closes) return { when, ...CLOSE_WORDS[branch.closes] };
  const targets = branch.targets
    .map((target) => {
      const next = flow.steps.find((candidate) => candidate.id === target.stepId);
      return next ? stepTitle(next) : target.stepId.replace(/^step:/, '');
    });
  if (targets.length === 0) return { when, then: 'Nothing follows', tone: 'end' };
  const joiner = targets.length > 1 ? ' and ' : '';
  return {
    when,
    then: targets.length > 1 ? `Runs ${targets.slice(0, -1).join(', ')}${joiner}${targets.at(-1)} in parallel` : `Goes to ${targets[0]}`,
    tone: 'go',
  };
}
