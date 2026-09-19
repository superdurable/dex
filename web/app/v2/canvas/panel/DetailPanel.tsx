// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useState, type JSX } from 'react'
import { storedValueJSONReplacer } from '@/lib/blobs'
import type { PanelModel, Row, SectionId } from './panelModel'
import { AVAILABILITY_TEXT, VERDICT_TEXT } from './panelModel'

const KIND_GLYPH: Record<string, string> = {
  channel: '✉',
  timer: '⏱',
  subflow: '↳',
  unknown: '?',
}

function Rows({ rows }: { rows: Row[] }): JSX.Element {
  return (
    <dl className="ppan-rows">
      {rows.map((r, i) => (
        <div className="ppan-row" data-tone={r.tone} key={`${r.label}-${i}`}>
          <dt>{r.label}</dt>
          <dd className={r.pre ? 'ppan-pre' : undefined}>{r.value}</dd>
        </div>
      ))}
    </dl>
  )
}

function Payload({ value }: { value: unknown }): JSX.Element {
  if (value === undefined) {
    return <p className="ppan-note">No value on this execution.</p>
  }
  if (value && typeof value === 'object' && !Array.isArray(value) && (value as { unavailable?: unknown }).unavailable === true) {
    return <p className="ppan-note">Snapshot unavailable.</p>
  }
  return (
    <pre className="ppan-pre ppan-json">{JSON.stringify(value, storedValueJSONReplacer, 2)}</pre>
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
}): JSX.Element {
  const [open, setOpen] = useState<SectionId>(
    initialSection != null && model.sections.some((s) => s.id === initialSection)
      ? initialSection
      : model.defaultSection,
  )

  const section = model.sections.find((s) => s.id === open) ?? model.sections[0]

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

      <nav className="ppan-tabs">
        {model.sections.map((s) => (
          <button
            key={s.id}
            type="button"
            className="ppan-tab"
            data-on={s.id === open ? 'true' : undefined}
            onClick={() => {
              setOpen(s.id)
              onSection?.(s.id)
            }}
          >
            {s.label}
          </button>
        ))}
      </nav>

      <div className="ppan-body">
        {section === undefined ? null : section.body.kind === 'rows' ? (
          <>
            <Rows rows={section.body.rows} />
            {section.body.availability !== undefined &&
            section.body.availability !== 'available' ? (
              <p className="ppan-note">{AVAILABILITY_TEXT[section.body.availability]}</p>
            ) : null}
          </>
        ) : section.body.kind === 'payload' ? (
          <Payload value={section.body.value} />
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
                {section.body.rows.map((r) => (
                  <tr key={`${r.kind}-${r.label}`} data-verdict={r.verdict}>
                    <td>
                      <span className="ppan-glyph">{KIND_GLYPH[r.kind] ?? '?'}</span> {r.label}
                    </td>
                    <td>{VERDICT_TEXT[r.verdict]}</td>
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
        ) : section.body.kind === 'execute' ? (
          <>
            {section.body.attempts.length === 0 ? (
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
                  {section.body.attempts.map((a) => (
                    <tr key={a.n}>
                      <td>{a.n}</td>
                      <td>{a.status}</td>
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
                  label: 'Goes to',
                  value: section.body.targets.length > 0 ? section.body.targets.join(', ') : 'nothing — this closes',
                },
              ]}
            />
            {section.body.defaultTab === 'error' && section.body.error !== undefined ? (
              <>
                <h3>Error</h3>
                <p className="ppan-pre ppan-error">{section.body.error}</p>
              </>
            ) : section.body.io !== 'available' ? (
              <>
                <h3>Output</h3>
                <p className="ppan-note">{AVAILABILITY_TEXT[section.body.io]}</p>
              </>
            ) : null}
          </>
        ) : section.body.kind === 'executions' ? (
          <table className="ppan-table">
            <thead>
              <tr>
                <th>Execution</th>
                <th>Wait</th>
                <th>Execute</th>
                <th>Won by</th>
              </tr>
            </thead>
            <tbody>
              {section.body.rows.map((r) => (
                <tr
                  key={r.id}
                  className="ppan-clickable"
                  data-on={r.id === selectedExecutionId ? 'true' : undefined}
                  onClick={() => onSelectExecution(r.id)}
                >
                  <td>{r.id}</td>
                  <td>{r.wait}</td>
                  <td>{r.execute}</td>
                  <td>{r.won}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <>
            <h3>Entered from</h3>
            {section.body.inbound.length === 0 ? (
              <p className="ppan-note">Nothing transitions into this step.</p>
            ) : (
              <Rows rows={section.body.inbound} />
            )}
            <h3>Exits to</h3>
            {section.body.outbound.length === 0 ? (
              <p className="ppan-note">This step does not transition anywhere.</p>
            ) : (
              <Rows rows={section.body.outbound} />
            )}
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
    </aside>
  )
}
