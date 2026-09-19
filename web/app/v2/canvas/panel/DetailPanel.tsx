// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useState, type JSX } from 'react'
import {
  StepMethodContextSection,
  StepMethodInputSection,
  StepMethodOutputSection,
} from '@/app/flows/details/StepMethodSections'
import { storedValueJSONReplacer } from '@/lib/blobs'
import type { FlowHistoryEvent } from '@/lib/types'
import type { PanelModel, Row, SectionId } from './panelModel'
import { VERDICT_TEXT } from './panelModel'

const KIND_GLYPH: Record<string, string> = {
  channel: '✉',
  timer: '⏱',
  subflow: '↳',
  unknown: '?',
}

function Rows({ rows }: { rows: Row[] }): JSX.Element {
  return (
    <dl className="ppan-rows">
      {rows.map((row, index) => (
        <div className="ppan-row" data-tone={row.tone} key={`${row.label}-${index}`}>
          <dt>{row.label}</dt>
          <dd className={row.pre ? 'ppan-pre' : undefined}>{row.value}</dd>
        </div>
      ))}
    </dl>
  )
}

function StepMethodPane({
  part,
  event,
  history,
  parentFlowId,
}: {
  part: 'input' | 'output' | 'context'
  event: FlowHistoryEvent
  history: FlowHistoryEvent[]
  parentFlowId: string
}): JSX.Element {
  const [view, setView] = useState<'details' | 'raw'>('details')
  useEffect(() => {
    setView('details')
  }, [event.eventId, event.type, part])

  return (
    <div className="ppan-step-method">
      <div className="ppan-view-tabs" role="tablist" aria-label="Step method view">
        <button
          type="button"
          role="tab"
          aria-selected={view === 'details'}
          className={view === 'details' ? 'active' : undefined}
          onClick={() => setView('details')}
        >
          Details
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={view === 'raw'}
          className={view === 'raw' ? 'active' : undefined}
          onClick={() => setView('raw')}
        >
          Raw JSON
        </button>
      </div>
      {view === 'raw' ? (
        <pre className="ppan-pre ppan-json">
          {JSON.stringify(event.payload, storedValueJSONReplacer, 2)}
        </pre>
      ) : (
        <div className="semantic-event ppan-semantic">
          {part === 'input' ? (
            <StepMethodInputSection
              event={event}
              history={history}
              parentFlowId={parentFlowId}
              wrapSection={false}
            />
          ) : part === 'output' ? (
            <StepMethodOutputSection
              event={event}
              history={history}
              parentFlowId={parentFlowId}
              wrapSection={false}
            />
          ) : (
            <StepMethodContextSection
              event={event}
              history={history}
              parentFlowId={parentFlowId}
              wrapSection={false}
            />
          )}
        </div>
      )}
    </div>
  )
}

export function DetailPanel({
  model,
  onClose,
  onSelectExecution,
  selectedExecutionId,
  initialSection,
  onSection,
  canLoadPrevious = false,
  loadPreviousBusy = false,
  previousEmpty = false,
  onLoadPrevious,
  methodEvent = null,
  history = [],
  parentFlowId = '',
}: {
  model: PanelModel
  onClose: () => void
  onSelectExecution: (id: string) => void
  selectedExecutionId: string | null
  initialSection?: SectionId | null
  onSection?: (id: SectionId) => void
  canLoadPrevious?: boolean
  loadPreviousBusy?: boolean
  previousEmpty?: boolean
  onLoadPrevious?: () => void
  methodEvent?: FlowHistoryEvent | null
  history?: FlowHistoryEvent[]
  parentFlowId?: string
}): JSX.Element {
  const [open, setOpen] = useState<SectionId>(
    initialSection != null && model.sections.some((section) => section.id === initialSection)
      ? initialSection
      : model.defaultSection,
  )
  const [definitionOpen, setDefinitionOpen] = useState(true)
  const [branchesExpanded, setBranchesExpanded] = useState(false)

  useEffect(() => {
    if (initialSection != null && model.sections.some((section) => section.id === initialSection)) {
      setOpen(initialSection)
    }
  }, [initialSection, model.sections])

  useEffect(() => {
    setBranchesExpanded(false)
  }, [model.stepType])

  const section = model.sections.find((candidate) => candidate.id === open) ?? model.sections[0]
  const selectedId = selectedExecutionId
    ?? model.executions[model.executions.length - 1]?.id
    ?? ''
  const branchCount = model.definition.branches.length
  const branchesCollapsed = branchCount > 3 && !branchesExpanded

  return (
    <aside className="ppan" aria-label={`Detail for ${model.stepType}`}>
      <header className="ppan-head">
        <div>
          <h2>{model.title}</h2>
          <p className="ppan-type">{model.stepType}</p>
        </div>
        <button type="button" className="ppan-close" onClick={onClose} aria-label="Close panel">
          ✕
        </button>
      </header>

      <section className="ppan-def" aria-label="Step definition">
        <button
          type="button"
          className="ppan-def-toggle"
          aria-expanded={definitionOpen}
          onClick={() => setDefinitionOpen((openNow) => !openNow)}
        >
          <span>Definition</span>
          <span className="ppan-def-chevron">{definitionOpen ? '▾' : '▸'}</span>
        </button>
        {definitionOpen ? (
          <div className="ppan-def-body">
            <h3>Explanation</h3>
            {model.definition.explanation === null ? (
              <p className="ppan-note">No dex:explanation on this Step.</p>
            ) : (
              <p className="ppan-def-explanation">{model.definition.explanation}</p>
            )}
            <h3>WaitFor</h3>
            {model.definition.waitFor === null ? (
              <p className="ppan-note">No WaitFor — Execute runs immediately.</p>
            ) : (
              <>
                <p className="ppan-def-line">{model.definition.waitFor}</p>
                {model.definition.waitConditions.length > 0 ? (
                  <ul className="ppan-def-list">
                    {model.definition.waitConditions.map((condition) => (
                      <li key={condition}>{condition}</li>
                    ))}
                  </ul>
                ) : null}
              </>
            )}
            <h3>Execute branches</h3>
            {branchCount === 0 ? (
              <p className="ppan-note">No Execute branches in the definition.</p>
            ) : branchesCollapsed ? (
              <button
                type="button"
                className="ppan-branches-toggle"
                onClick={() => setBranchesExpanded(true)}
              >
                {branchCount} branches · expand
              </button>
            ) : (
              <>
                <Rows rows={model.definition.branches} />
                {branchCount > 3 ? (
                  <button
                    type="button"
                    className="ppan-branches-toggle"
                    onClick={() => setBranchesExpanded(false)}
                  >
                    Collapse branches
                  </button>
                ) : null}
              </>
            )}
            {model.definition.source !== null ? (
              <>
                <h3>Source</h3>
                <p className="ppan-def-line ppan-def-source">{model.definition.source}</p>
              </>
            ) : null}
          </div>
        ) : null}
      </section>

      <section className="ppan-exec" aria-label="Step execution">
        <div className="ppan-exec-head">
          <h3>Execution</h3>
          {model.executions.length > 1 ? (
            <label className="ppan-exec-pick">
              <span>Instance</span>
              <select
                aria-label="Step execution"
                value={selectedId}
                onChange={(event) => onSelectExecution(event.target.value)}
              >
                {model.executions.map((execution, index) => (
                  <option key={execution.id} value={execution.id}>
                    {`${index + 1}/${model.executions.length} · ${execution.id} · ${execution.execute}`}
                  </option>
                ))}
              </select>
            </label>
          ) : model.executions.length === 1 ? (
            <p className="ppan-exec-one" title={model.executions[0].id}>
              {model.executions[0].id}
            </p>
          ) : (
            <p className="ppan-note">No executions loaded for this Step.</p>
          )}
        </div>

        <nav className="ppan-tabs">
          {model.sections.map((candidate) => (
            <button
              key={candidate.id}
              type="button"
              className="ppan-tab"
              data-on={candidate.id === open ? 'true' : undefined}
              onClick={() => {
                setOpen(candidate.id)
                onSection?.(candidate.id)
              }}
            >
              {candidate.label}
            </button>
          ))}
        </nav>

        <div className="ppan-body">
          {section === undefined ? null : section.body.kind === 'rows' ? (
            <Rows rows={section.body.rows} />
          ) : section.body.kind === 'stepMethod' && methodEvent !== null ? (
            <StepMethodPane
              part={section.body.part}
              event={methodEvent}
              history={history.length > 0 ? history : [methodEvent]}
              parentFlowId={parentFlowId}
            />
          ) : section.body.kind === 'stepMethod' ? (
            <p className="ppan-note">No WaitFor or Execute event is loaded for this execution.</p>
          ) : section.body.kind === 'wait' ? (
            <>
              <table className="ppan-table">
                <thead>
                  <tr>
                    <th>Condition</th>
                    <th>Verdict</th>
                  </tr>
                </thead>
                <tbody>
                  {section.body.rows.map((row) => (
                    <tr key={`${row.kind}-${row.label}`} data-verdict={row.verdict}>
                      <td>
                        <span className="ppan-glyph">{KIND_GLYPH[row.kind] ?? '?'}</span> {row.label}
                      </td>
                      <td>{VERDICT_TEXT[row.verdict]}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {section.body.winner !== null ? (
                <p className="ppan-note">
                  Satisfied by <strong>{section.body.winner}</strong>. Dex consumes only from the
                  condition that won, so anything the losers had queued stays queued.
                </p>
              ) : null}
              {section.body.answeredBy.length > 0 ? (
                <p className="ppan-note">
                  Answered through <strong>{section.body.answeredBy.join(', ')}</strong>.
                </p>
              ) : null}
            </>
          ) : (
            <>
              {!section.body.hasExecuteEvent ? (
                <p className="ppan-note">
                  Loaded history has no Execute event for this Step yet.
                  {canLoadPrevious
                    ? ' Use Load more from previous run if Continue-as-New moved it.'
                    : ''}
                </p>
              ) : section.body.attempts.length === 0 ? (
                <p className="ppan-note">This step has not run in this run.</p>
              ) : (
                <table className="ppan-table">
                  <thead>
                    <tr>
                      <th>Attempt</th>
                      <th>Outcome</th>
                    </tr>
                  </thead>
                  <tbody>
                    {section.body.attempts.map((attempt) => (
                      <tr key={attempt.n}>
                        <td>{attempt.n}</td>
                        <td>{attempt.status}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              <Rows
                rows={[
                  { label: 'Retries', value: section.body.attemptsLeft },
                  { label: 'Decision', value: section.body.decision ?? 'not returned yet' },
                  {
                    label: 'Next steps',
                    value:
                      section.body.nextStepTypes.length > 0
                        ? section.body.nextStepTypes.join(', ')
                        : section.body.hasExecuteEvent
                          ? 'none — this closes'
                          : '—',
                  },
                ]}
              />
              {section.body.defaultTab === 'error' && section.body.error !== undefined ? (
                <>
                  <h3>Error</h3>
                  <p className="ppan-pre ppan-error">{section.body.error}</p>
                </>
              ) : null}
            </>
          )}

          {canLoadPrevious ? (
            <div className="ppan-more">
              <button
                type="button"
                className="pchip"
                disabled={loadPreviousBusy || onLoadPrevious === undefined}
                onClick={onLoadPrevious}
              >
                {loadPreviousBusy ? 'Loading…' : 'Load more from previous run'}
              </button>
              {previousEmpty ? (
                <p className="ppan-note">That earlier run has no executions of this step.</p>
              ) : (
                <p className="ppan-note">
                  Continue-as-New keeps the Flow ID. This loads only this step from the previous run.
                </p>
              )}
            </div>
          ) : null}
        </div>
      </section>
    </aside>
  )
}
