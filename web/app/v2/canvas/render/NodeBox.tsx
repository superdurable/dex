// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { JSX, KeyboardEvent } from 'react'
import type { PhaseCell } from '../model/run'
import type { Box } from '../views/types'

/**
 * One box, as plain DOM. Nothing here knows about React Flow, so a Scene is renderable in a test or
 * under a different renderer.
 *
 * TIER-BLIND on purpose: no `detail` prop and no branch on it. Detail lives as one attribute on the
 * pane, so changing it is a single mutation and zero box re-renders.
 *
 * A `div[role=button]` rather than a `<button>`, because the segmented bar inside is itself a real
 * button and nesting interactive controls is invalid. Enter and Space are wired by hand to keep what
 * a native button would have given for free.
 */

/** Which condition kind the Wait cell is about. `mixed` when a StepType's executions differed. */
const KIND_GLYPH: Record<string, string> = {
  channel: '✉',
  timer: '⏱',
  subflow: '↳',
  unknown: '?',
  mixed: '✳',
}

const KIND_WORD: Record<string, string> = {
  channel: 'someone outside the flow must publish',
  timer: 'time must pass',
  subflow: 'a child flow must finish',
  unknown: 'the analyser did not say',
  mixed: 'different runs resolved differently — click to break it down',
}

function PhaseChip({ cell }: { cell: PhaseCell }): JSX.Element {
  const glyph = cell.phase === 'wait' ? (KIND_GLYPH[cell.kind ?? 'unknown'] ?? '?') : '▸'
  const what =
    cell.phase === 'wait'
      ? `WaitFor · ${cell.status} · ${KIND_WORD[cell.kind ?? 'unknown'] ?? ''}`
      : `Execute · ${cell.status}`
  return (
    <span
      className="ptok-cell"
      data-phase={cell.phase}
      data-status={cell.status}
      data-achromatic={cell.achromatic ? 'true' : undefined}
      title={what}
    >
      {glyph}
    </span>
  )
}

function ConnectorIcon(): JSX.Element {
  return (
    <svg
      aria-label="Connector Step"
      className="pbox-connector-icon"
      role="img"
      viewBox="0 0 16 16"
    >
      <path d="M5 1.5v3M11 1.5v3M3.5 4.5h9v2A4.5 4.5 0 0 1 8 11H7v3.5" />
    </svg>
  )
}

export function NodeBox({
  box,
  selected,
  hovered,
  referenced,
  onSelect,
  onHover,
  onInspect,
}: {
  box: Box
  selected: boolean
  /**
   * True when the pointer is over this card OR over a reference to it somewhere else — a chip in the
   * transcript, a name in a panel. That is why it is a prop rather than plain CSS `:hover`: `:hover`
   * only knows about the pointer being here, and the point of the mark is to answer "which box is the
   * thing chat just mentioned".
   */
  hovered?: boolean
  /**
   * This card is in the composer's reference list.
   *
   * A THIRD state, distinct from selected and hovered, because it answers a different question. Selected
   * is "what the panel is describing" and is singular; referenced is "what the next message is about" and
   * is plural. A gesture that built a list without marking its members would be a list you cannot check.
   */
  referenced?: boolean
  /** `additive` is Cmd (macOS) or Ctrl, and means "add to the reference list" rather than "select". */
  onSelect?: (id: string, additive: boolean) => void
  /** Published so a reference elsewhere can light up when the pointer is over the card. */
  onHover?: (id: string | null) => void
  /** The bar's click: "enumerate every execution of this StepType". */
  onInspect?: (id: string) => void
}): JSX.Element {
  /**
   * `metaKey || ctrlKey`, so one handler serves both platforms.
   *
   * Cmd on macOS and Ctrl elsewhere is the extend-selection convention everywhere it exists, and reading
   * both means no platform sniffing. Ctrl-click on macOS also raises a context menu, which is harmless
   * here: the card has no menu of its own, so the worst case is the browser's showing beside a toggle
   * that did what Cmd-click would have done.
   */
  const additive = (e: { metaKey: boolean; ctrlKey: boolean }): boolean => e.metaKey || e.ctrlKey

  const activate = (e: { metaKey: boolean; ctrlKey: boolean }): void =>
    onSelect?.(box.id, additive(e))
  const onKey = (e: KeyboardEvent<HTMLDivElement>): void => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      activate(e)
    }
  }
  /**
   * Pointer events, not mouse events, so a pen or a touch drag reports the same way. `onPointerLeave`
   * clears with `null` rather than with this box's id: two cards' enter and leave can interleave, and
   * clearing by id would let the card being left erase the highlight on the card being entered.
   */
  const hoverProps = {
    onPointerEnter: () => onHover?.(box.id),
    onPointerLeave: () => onHover?.(null),
    onFocus: () => onHover?.(box.id),
    onBlur: () => onHover?.(null),
  }

  /**
   * An external RPC has its own minimal shape, and needs a nested layer to have a border at all.
   *
   * `clip-path` clips a `border` away with everything else, so the pennant's outline is the OUTER
   * element showing through 1.5px around an inset inner one. Returned early rather than threaded
   * through the Step markup because none of it applies: an RPC has no phases, no execution bar and no
   * anatomy — it is a door, and the only facts about it are its name and how many gates it opens.
   */
  if (box.kind === 'rpc') {
    return (
      <div
        role="button"
        tabIndex={0}
        className="pbox pbox-rpc nodrag nopan"
        data-point={box.point ?? 'right'}
        data-selected={selected ? 'true' : undefined}
        data-hover={hovered === true ? 'true' : undefined}
        data-ref={referenced === true ? 'true' : undefined}
        style={{ width: `${box.w}px`, height: `${box.h}px` }}
        title={`${box.title} — an external call. ${box.subtitle ?? ''}`}
        onClick={activate}
        onKeyDown={onKey}
        {...hoverProps}
      >
        <span className="pbox-inner">
          <span className="pbox-title">{box.title}</span>
          {box.subtitle ? <span className="pbox-sub">{box.subtitle}</span> : null}
        </span>
      </div>
    )
  }

  return (
    <div
      role="button"
      tabIndex={0}
      className={`pbox pbox-${box.kind} nodrag nopan`}
      data-actor={box.actor}
      /* An agentic role is a SILHOUETTE concern, so it reaches CSS as its own attribute rather than being
         folded into the actor accent: a card can be both a human gate and a person's responsibility. */
      data-agent={box.agentRole}
      /* Where the Flow begins is a definition fact, so it does not compete with run state for
         `data-emphasis`. Both can be true at once and both now paint. */
      data-start={box.isStart ? 'true' : undefined}
      data-emphasis={box.emphasis}
      data-selected={selected ? 'true' : undefined}
      data-hover={hovered === true ? 'true' : undefined}
      data-ref={referenced === true ? 'true' : undefined}
      style={{ width: `${box.w}px`, height: `${box.h}px` }}
      onClick={activate}
      onKeyDown={onKey}
      {...hoverProps}
    >
      <span className="pbox-head">
        <span className="pbox-title-line">
          {box.icon === 'connector' ? <ConnectorIcon /> : null}
          <span className="pbox-title">{box.title}</span>
        </span>
        {/*
          BPMN's LOOP MARKER. BPMN puts ↻ on the activity itself rather than relying on the reader
          tracing a back edge, and that is the right call here for the same reason: the arc is a few
          pixels at a small zoom, and "this repeats" is not a fact to make someone hunt for.
        */}
        {box.loops === true ? (
          <span className="pbox-loop" title="This step can run again — it transitions to itself">
            ↻
          </span>
        ) : null}
        {/* Which kind of failure target, when it is one. Independent of the badge — a gate can be both. */}
        {box.recovery ? (
          <span className="pbox-recovery" title={box.recovery.title}>
            {box.recovery.glyph}
          </span>
        ) : null}
        {box.badge ? <span className="pbox-badge">{box.badge}</span> : null}
      </span>

      <span className="pbox-meta">
        {/* The TYPE, never a value — Airflow's operator subtitle, n8n's operation subtitle. */}
        {box.subtitle ? <span className="pbox-sub">{box.subtitle}</span> : null}

        {/*
          ONE token slot, at most TWO cells. The Wait cell is ABSENT when the Step has no WaitFor, so a
          single-phase Step's card looks like every other product's single-token card and we only pay
          the complexity where the model actually has it.
        */}
        {box.token ? (
          <span className="ptok" data-aggregate={box.token.aggregate}>
            {box.token.wait ? <PhaseChip cell={box.token.wait} /> : null}
            <PhaseChip cell={box.token.execute} />
            {box.status ? <span className="ptok-count">{box.status}</span> : null}
          </span>
        ) : box.status ? (
          <span className="pbox-status">{box.status}</span>
        ) : null}
      </span>

      {box.rows && box.rows.length > 0 ? (
        <span className="pbox-rows">
          {box.rows.map((r, i) => (
            <span className="pbox-row" data-tone={r.tone} key={`${r.text}-${i}`}>
              {r.glyph ? <span className="pbox-glyph">{r.glyph}</span> : null}
              <span className="pbox-text">{r.text}</span>
              {r.trail ? <span className="pbox-trail">{r.trail}</span> : null}
            </span>
          ))}
        </span>
      ) : null}

      {/*
        The proportional state bar, and a real hit target rather than a picture. Each segment's width
        IS the count with a 2px floor, so one failure among many survives; hovering decodes it into
        counts; clicking enumerates the executions. A bar you cannot interrogate only looks informative.
      */}
      {box.bar && box.bar.length > 0 ? (
        <button
          type="button"
          className="pbox-bar"
          title={box.barTitle}
          aria-label={box.barTitle ?? 'execution outcomes'}
          onClick={(e) => {
            e.stopPropagation()
            // Never additive: the bar's job is "show me every execution of this", which is a request to
            // INSPECT one step, and inspecting is singular.
            onSelect?.(box.id, false)
            onInspect?.(box.id)
          }}
        >
          {box.bar.map((seg) => (
            <span
              className="pbox-seg"
              data-status={seg.status}
              key={seg.status}
              style={{ flexGrow: seg.count }}
            />
          ))}
        </button>
      ) : null}

      {box.sections?.map((s) => (
        <span className="pbox-section" key={s.label}>
          <span className="pbox-section-label">{s.label}</span>
          {s.rows.map((r, i) => (
            <span className="pbox-row" data-tone={r.tone} key={`${r.text}-${i}`}>
              {r.glyph ? <span className="pbox-glyph">{r.glyph}</span> : null}
              <span className="pbox-text">{r.text}</span>
              {r.trail ? <span className="pbox-trail">{r.trail}</span> : null}
            </span>
          ))}
        </span>
      ))}

      {/*
        ONE line of templated prose, and only when blocked or failed — so a healthy Step has no strip
        and a green run reads quiet. Templated rather than generated because it sits on the canvas
        where it cannot carry a caveat; generated explanation belongs in the panel, one click away.
      */}
      {box.reason ? (
        <span className="pbox-reason" data-tone={box.reason.tone} title={box.reason.text}>
          {box.reason.text}
        </span>
      ) : null}
    </div>
  )
}
