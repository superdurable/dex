// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  Background,
  BackgroundVariant,
  Controls,
  type Edge,
  Handle,
  type Node,
  type NodeProps,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useReactFlow,
} from '@xyflow/react'
import {
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  type JSX,
  type KeyboardEvent,
  type MouseEvent as ReactMouseEvent,
  type Ref,
} from 'react'
import type { Box, Detail, Direction, Link, Scene } from '../views/types'
import { LEGEND_BOX_H, LEGEND_W, legendAnchor } from './legendAnchor'
import type { CanvasViewportHandle } from './viewport'
import { EDGE_MARKER, RPC_TAIL } from './markers'
import { NodeBox } from './NodeBox'
import {
  chromeCompensation,
  edgeContrastBoost,
  FIT_MAX_ZOOM,
  FOCUS_ZOOM,
  MAX_ZOOM,
  MIN_ZOOM,
} from './zoom'
import '@xyflow/react/dist/style.css'

/**
 * THE ONLY FILE THAT IMPORTS @xyflow.
 *
 * Everything above this consumes a Scene, which is plain geometry. Zoom here is PURELY GEOMETRIC —
 * level of detail arrives as a prop and is decided by an explicit control, never derived from the
 * viewport.
 */

/**
 * Handles live here, not in `NodeBox`.
 *
 * React Flow anchors every edge to a Handle and silently drops any edge whose endpoint has none —
 * which is exactly what happened on the first run: correct geometry, correct edge list, and not one
 * arrow on screen.
 *
 * Two named channels: control flow routes vertically, everything else sideways, so a failure path
 * or a gate link never competes with a transition for the same anchor.
 */
function BoxNode({ data }: NodeProps): JSX.Element {
  const d = data as unknown as {
    box: Box
    selected: boolean
    hovered: boolean
    referenced: boolean
    onSelect?: (id: string, additive: boolean) => void
    onHover?: (id: string | null) => void
    onInspect?: (id: string) => void
  }
  return (
    <>
      <Handle type="target" id="v" position={Position.Top} className="phandle" />
      <Handle type="target" id="h" position={Position.Left} className="phandle" />
      {/*
        Far-side target faces, so an edge can arrive from BEHIND a node instead of tunnelling through
        the one in front of it. Used by `route: 'around'`.
      */}
      <Handle type="target" id="vb" position={Position.Bottom} className="phandle" />
      <Handle type="target" id="hr" position={Position.Right} className="phandle" />
      <NodeBox
        box={d.box}
        selected={d.selected}
        hovered={d.hovered}
        referenced={d.referenced}
        onSelect={d.onSelect}
        onHover={d.onHover}
        onInspect={d.onInspect}
      />
      <Handle type="source" id="v" position={Position.Bottom} className="phandle" />
      <Handle type="source" id="h" position={Position.Right} className="phandle" />
    </>
  )
}

function BandNode({ data }: NodeProps): JSX.Element {
  const d = data as unknown as {
    id: string
    label?: string
    style: 'lane' | 'frame' | 'group'
    tone?: string
    hue?: number
    w: number
    h: number
    selected?: boolean
    onSelect?: (id: string, additive: boolean) => void
  }
  /**
   * A group band is a TAP TARGET, and only in the space outside the cards.
   *
   * Cards sit above it, so a click on a Step still reaches the Step; a click on the label strip or the
   * padding around the members reaches the group. That gesture is what lets a group's description leave
   * the sidebar — "tell me about this region" becomes something you do on the drawing rather than
   * another block of prose beside it.
   */
  const clickable = d.style === 'group' && d.onSelect !== undefined
  // The modifier has to reach the caller, exactly as it does for a card: a region IS an entity, so
  // Cmd-clicking one must reference it. Dropping the event here meant a band could only ever open its panel.
  const additive = (e: { metaKey: boolean; ctrlKey: boolean }): boolean => e.metaKey || e.ctrlKey
  return (
    <div
      className={`pband pband-${d.style}${clickable ? ' pband-tap' : ''}`}
      data-tone={d.tone}
      data-hue={d.hue === undefined ? undefined : d.hue % 6}
      data-selected={d.selected ? 'true' : undefined}
      style={{ width: d.w, height: d.h }}
      {...(clickable
        ? {
            role: 'button',
            tabIndex: 0,
            title: `${d.label ?? 'Group'} — click for what this region is`,
            onClick: (e: ReactMouseEvent<HTMLDivElement>) => d.onSelect?.(d.id, additive(e)),
            onKeyDown: (e: KeyboardEvent<HTMLDivElement>) => {
              if (e.key !== 'Enter' && e.key !== ' ') return
              e.preventDefault()
              d.onSelect?.(d.id, additive(e))
            },
          }
        : {})}
    >
      {d.label ? <span className="pband-label">{d.label}</span> : null}
    </div>
  )
}

/**
 * The legend, as a NODE.
 *
 * It used to be a window-fixed overlay, which meant it never moved when the drawing did and could sit on
 * top of it. Placed in scene coordinates it belongs to the diagram: it pans and zooms with what it
 * decodes, and a reader who scrolls to a region takes the key with them.
 */
function LegendNode({ data }: NodeProps): JSX.Element {
  const d = data as unknown as { render: () => JSX.Element }
  return <div className="plegend-node nodrag nopan">{d.render()}</div>
}

const NODE_TYPES = { pbox: BoxNode, band: BandNode, legend: LegendNode }

/**
 * THE FIT PADDING, with the right-hand panel's width subtracted.
 *
 * The panel is an absolute overlay ON TOP of the React Flow pane, so the pane's width includes the area
 * underneath it and `fitView` fits to a viewport a quarter wider than what you can actually see. That was a
 * documented gap while the panel only opened on a click; in run mode it opens by default, so the execution
 * map was being drawn correctly and running off under the panel every time.
 *
 * Asymmetric padding fixes it without relaying out the pane: `right` reserves the occluded strip, so the
 * fit is against the visible area. A percentage rather than the pixel width because the pane's width varies
 * with the chat split, and 26% is `.ppan`'s 360px against a typical canvas pane.
 */
/**
 * `top` clears the fixture picker, which is an absolute overlay at the top of the pane rather than part of
 * the layout — so a fit that used an even padding put the first Step underneath it.
 */
const FIT_BASE = {
  padding: { top: '8%', bottom: '4%', left: '4%', right: '4%' },
  minZoom: MIN_ZOOM,
  maxZoom: FIT_MAX_ZOOM,
} as const

function fitOptions(insetRightPx: number | null | undefined, paneWidth: number) {
  if (insetRightPx == null || insetRightPx <= 0 || paneWidth <= 0) return FIT_BASE
  const rightPct = Math.min(58, Math.max(18, (insetRightPx / paneWidth) * 100 + 4))
  return {
    padding: { top: '8%', bottom: '4%', left: '4%', right: `${rightPct}%` },
    minZoom: MIN_ZOOM,
    maxZoom: FIT_MAX_ZOOM,
  } as const
}

function Inner({
  scene,
  detail,
  direction,
  dimUnrelated = true,
  focusNodeId,
  insetRightPx,
  legend,
  handleRef,
  onSelectGroup,
  selectedGroupId,
  selectedId,
  hoveredId,
  referencedIds,
  onSelect,
  onHover,
  onInspect,
  interactive,
  fitKey,
}: {
  scene: Scene
  detail: Detail
  /** Needed for edge routing: which axis the ranks advance along swaps with it. */
  direction: Direction
  selectedId: string | null
  /**
   * The entity the pointer is over, wherever it is — a card here, or a chip in the transcript.
   *
   * A PROP, not a context read at the leaf, which is a deliberate trade. Reading it inside `NodeBox`
   * would confine a chat-side hover to one card's re-render, but it would also make the canvas layer
   * import the app's hooks, and that layer depending only on react/@xyflow/dagre is what let it move out
   * of the harness in one piece. A hover re-renders ~20 nodes of pure geometry; the boundary is worth
   * more than the frames.
   */
  hoveredId?: string | null
  /** Ids the composer is carrying. Marked on the card so a list built by clicking can be checked. */
  referencedIds?: readonly string[]
  onSelect?: (id: string | null, additive: boolean) => void
  onHover?: (id: string | null) => void
  /** "enumerate every execution of this StepType" — raised by the segmented bar. */
  onInspect?: (id: string) => void
  /** Rendered as a node at the diagram's top-left, so it zooms and pans with the drawing. */
  legend?: () => JSX.Element
  /** False when the selection was made for the reader, so the graph is not faded for them. */
  dimUnrelated?: boolean
  /** When set, fit targets this node at reading zoom instead of the whole graph. */
  focusNodeId?: string | null
  /** Publishes fit and zoom so a keyboard shortcut has something to drive. */
  handleRef?: Ref<CanvasViewportHandle>
  /** A click inside a group band's own space, outside any card. `additive` is Cmd (macOS) or Ctrl. */
  onSelectGroup?: (id: string | null, additive: boolean) => void
  selectedGroupId?: string | null
  interactive: boolean
  /**
   * Pixel width of the right-hand panel overlay, or null when none is open.
   *
   * A prop rather than something measured, because only the app knows a panel is open — and measuring the
   * overlay from in here would make the renderer depend on the shape of the thing overlaying it.
   */
  insetRightPx?: number | null
  /** Changing this refits the view. Deliberately NOT the zoom, or fit would fight the user. */
  fitKey: string
}): JSX.Element {
  const rf = useReactFlow()
  const paneRef = useRef<HTMLDivElement | null>(null)

  const nodes = useMemo<Node[]>(() => {
    /**
     * Dimensions are declared, not measured. The Scene already knows every box's size, and React
     * Flow needs sizes to compute the bounds `fitView` works from — left to measure the DOM it fits
     * against zero-size nodes on first paint.
     */
    const bands: Node[] = scene.bands.map((b) => ({
      id: `band:${b.id}`,
      type: 'band',
      position: { x: b.x, y: b.y },
      width: b.w,
      height: b.h,
      data: {
        id: b.id,
        label: b.label,
        style: b.style,
        tone: b.tone,
        hue: b.hue,
        w: b.w,
        h: b.h,
        selected: b.id === selectedGroupId,
        onSelect:
          b.style === 'group'
            ? (id: string, additive: boolean) => onSelectGroup?.(id, additive)
            : undefined,
      },
      draggable: false,
      selectable: false,
      zIndex: 0,
      style: { zIndex: 0 },
    }))
    const boxes: Node[] = scene.boxes.map((b) => ({
      id: b.id,
      type: 'pbox',
      position: { x: b.x, y: b.y },
      width: b.w,
      height: b.h,
      data: {
        box: b,
        selected: b.id === selectedId,
        hovered: b.id === hoveredId,
        referenced: referencedIds !== undefined && referencedIds.includes(b.id),
        onSelect: (id: string, additive: boolean) => onSelect?.(id, additive),
        onHover: (id: string | null) => onHover?.(id),
        onInspect: (id: string) => onInspect?.(id),
      },
      draggable: false,
      zIndex: 2,
      style: { zIndex: 2 },
    }))
    /**
     * Above and left of everything else, so it never covers a Step. Width is declared and height is
     * left to be measured — the pane already refits on resize, which is what settles it.
     */
    /**
     * BOTTOM-RIGHT of the drawing, bottom-aligned with the lowest box.
     *
     * Beside the content rather than over it, and anchored to the content's own extent rather than to
     * the window — so it reads as part of the drawing and moves when the drawing does. Anchoring the
     * BOTTOM is what makes it look placed rather than attached: a legend whose top is pinned drifts
     * away from the flow as the flow gets shorter.
     */
    const legendNode: Node[] = []
    const anchor = legendAnchor(scene.boxes, scene.bands)
    if (legend !== undefined && anchor !== undefined) {
      legendNode.push({
        id: '__legend',
        type: 'legend',
        position: anchor,
        width: LEGEND_W,
        height: LEGEND_BOX_H,
        data: { render: legend },
        draggable: false,
        selectable: false,
        zIndex: 3,
        style: { zIndex: 3, width: LEGEND_W, height: LEGEND_BOX_H },
      })
    }
    return [...bands, ...boxes, ...legendNode]
  }, [
    scene,
    selectedId,
    hoveredId,
    referencedIds,
    onSelect,
    onHover,
    onInspect,
    selectedGroupId,
    onSelectGroup,
    legend,
  ])

  const edges = useMemo<Edge[]>(() => {
    const present = new Map(scene.boxes.map((b) => [b.id, b]))
    /**
     * Which handle channel an edge uses.
     *
     * Control flow goes vertically, because that is the reading direction. Failure paths ALWAYS go
     * sideways — that is the routing fix that let recovery stop being a toggle: a failure edge runs
     * down a side gutter instead of reaching backwards across the happy path. Self-loops and
     * upward edges go sideways for the same reason.
     */
    /**
     * Which handles an edge leaves and enters by.
     *
     * A SELF-LOOP leaves the RIGHT face and re-enters the TOP one. It used to use the same channel for
     * both ends, which on one node means right-handle to left-handle — a path with nowhere to go, so
     * React Flow collapsed it to a stub and 21 self-loops in the corpus were effectively invisible.
     * Different faces give smoothstep a real corner to route around, which is how n8n draws a
     * loop-back and how Step Functions draws a retry: an arc that visibly leaves and returns.
     */
    const lr = direction === 'lr'
    const handles = (l: Link): { source: string; target: string } => {
      const a = present.get(l.from)
      const b = present.get(l.to)
      if (a !== undefined && b !== undefined && a.id === b.id) {
        return { source: 'h', target: 'v' }
      }
      /**
       * AROUND, not through. An external call with several targets in one row has to get past the
       * nearest of them to reach the rest, and running straight across meant the segment that emerged
       * between two cards read as an edge BETWEEN them — a relationship that does not exist, which is
       * worse than a crossing. So it leaves by the along-axis face and arrives at the far side.
       */
      if (l.route === 'around') {
        return lr ? { source: 'h', target: 'hr' } : { source: 'v', target: 'vb' }
      }
      // Explicitly routed aside by the view because it would otherwise tunnel under the cards
      // between its endpoints.
      /**
       * The CROSS channel is whichever one the ranks do not advance along — sideways top-down, and
       * vertical left-right. It was hardcoded to `h`, which top-down is correct and left-right is the
       * FORWARD channel, so every edge meant to go around instead ran along the ranks and straight
       * through them. The real book pipeline made that visible: 25 tunnelling edges left-right, because
       * eight failure paths converge on one recovery gate from all over a twelve-rank chain.
       */
      const cross: 'v' | 'h' = lr ? 'v' : 'h'
      if (l.route === 'side') return { source: cross, target: cross }
      /**
       * A MESSAGE FLOW ALWAYS TRAVELS IN THE GUTTER, whatever the vertical distance.
       *
       * The pennant lives in the across-axis gutter — left of the column top-down, above it
       * left-right — so its edge has to run ALONG the ranks inside that gutter until it is level with
       * its target, then turn in. That is the cross channel in both directions.
       *
       * This used to be positional, like a control edge, which was fine while every pennant sat beside
       * its one target. The real book pipeline broke it: one `approve` opens gates nine rows apart, so
       * `forward` was true for the distant ones, they left by the BOTTOM face aiming at the target's
       * TOP, and the horizontal leg of that path crossed the whole column of cards.
       */
      if (l.family !== 'control') {
        return { source: cross, target: cross }
      }
      if (a === undefined || b === undefined) return { source: 'v', target: 'v' }
      /**
       * FORWARD is measured on the axis the ranks advance along, which swaps with the direction. This
       * used to test `b.y > a.y` unconditionally — a top-down assumption. Left-right it made an edge
       * between two ranks at different across positions exit the BOTTOM face and enter the TOP one,
       * cutting through whatever sat between; the tunnelling metric caught 3 on the agent flow.
       */
      const forward = lr ? b.x > a.x + 8 : b.y > a.y + 8
      // Forward runs along the ranks; anything else takes the other channel and goes around.
      const ch: 'v' | 'h' = forward ? (lr ? 'h' : 'v') : lr ? 'v' : 'h'
      return { source: ch, target: ch }
    }
    return scene.links
      .filter((l) => present.has(l.from) && present.has(l.to))
      .filter((l) => {
        // Latent links are in the scene but quiet until an endpoint is selected. One Step in the
        // real flow absorbs 16 of 27 transitions; drawing them all is a star nobody can trace.
        if (l.latent !== true) return true
        return selectedId !== null && (l.from === selectedId || l.to === selectedId)
      })
      .map((l) => {
        const lit = selectedId !== null && (l.from === selectedId || l.to === selectedId)
        const h = handles(l)
        return {
          id: l.id,
          source: l.from,
          target: l.to,
          sourceHandle: h.source,
          targetHandle: h.target,
          type: 'smoothstep',
          /**
           * DIRECTION. Every edge had none until now — the graph was a set of bare lines, which is the
           * one thing a control view cannot afford to leave to inference.
           *
           * Filled for flow inside the process, hollow-plus-source-circle for a message from outside:
           * BPMN's sequence-flow / message-flow distinction, which is a SHAPE difference and therefore
           * survives the compare grid's thumbnails and any colour deficiency.
           *
           * A BARE ID, not `url(#id)`. React Flow wraps a string marker in `url(#…)` itself, so the
           * wrapped form became `url('#url(#p-arrow-rpc)')` — a valid attribute pointing at nothing,
           * which fails silently: no error, no warning, just edges with no arrowheads.
           */
          markerEnd: EDGE_MARKER[l.family],
          markerStart: l.family === 'rpc' || l.family === 'gate' ? RPC_TAIL : undefined,
          // Above the band nodes, which are opaque and would otherwise cover the edges.
          zIndex: 1,
          // Colour and width come from CSS so the zoom compensation variable can reach them
          // without re-rendering React on every wheel tick.
          /**
           * `pedge-path` is the live execution map's strongest signal, and it OUTRANKS the dim class: an
           * edge the run took stays bright even while a selection is dimming everything else, because
           * "where has this run been" must not be erasable by clicking a card.
           */
          className: `pedge pedge-${l.family}${l.selfLoop === true ? ' pedge-loop' : ''}${l.onPath === undefined ? '' : ' pedge-path'}${lit ? ' pedge-lit' : ''}${dimUnrelated && selectedId !== null && !lit && l.onPath === undefined ? ' pedge-dim' : ''}`,
          /**
           * The LABEL only — never `detail`. `detail` holds the full guard text for the panel, and
           * printing it here put source expressions back on the canvas by the side door.
           *
           * And only while LIT. An always-on variant was tried for case values and removed: the edge
           * runs through its own label, and in our data the value names its destination anyway. See
           * `views/shared.ts`.
           */
          label: lit ? l.label : undefined,
          labelStyle: { fill: 'var(--p-ink-1)', fontSize: 10 },
          labelBgStyle: { fill: 'var(--p-surface-2)' },
        }
      })
  }, [scene, selectedId, direction])

  /**
   * The imperative handle. `reveal` pans the minimum distance that brings a node into view, which is the
   * one operation React Flow does not offer directly.
   */
  useImperativeHandle(
    handleRef,
    () => ({
      zoomIn: () => void rf.zoomIn({ duration: 120 }),
      zoomOut: () => void rf.zoomOut({ duration: 120 }),
      fit: () => void rf.fitView(fitOptions(insetRightPx, paneRef.current?.clientWidth ?? 0)),
      zoom: () => rf.getZoom(),
      focus: (nodeId: string) => {
        const id = rf.getNode(nodeId) !== undefined ? nodeId : `band:${nodeId}`
        if (rf.getNode(id) === undefined) return
        void rf.fitView({ nodes: [{ id }], padding: 0.45, maxZoom: FOCUS_ZOOM, duration: 260 })
      },
      reveal: (nodeId: string) => {
        /**
         * A BAND IS REGISTERED UNDER A PREFIX, and resolving that is this function's job rather than the
         * caller's. Stage is the only minter of React Flow node ids and it gives bands `band:<id>` while
         * boxes keep their bare id — so a caller passing a band's scene id got `undefined` here and every
         * region reveal silently did nothing. Callers pass a SCENE id; only this file knows what became of it.
         */
        const id = rf.getNode(nodeId) !== undefined ? nodeId : `band:${nodeId}`
        if (rf.getNode(id) === undefined) return
        void rf.fitView({ nodes: [{ id }], padding: 0.4, maxZoom: rf.getZoom(), duration: 160 })
      },
    }),
    [rf, insetRightPx],
  )

  const focusNodeIdRef = useRef(focusNodeId)
  useEffect(() => { focusNodeIdRef.current = focusNodeId }, [focusNodeId])

  /**
   * Refit when the thing being shown changes — never on zoom.
   *
   * Driven by a ResizeObserver rather than a timer: a timer is a guess about when the browser has
   * sized the container, and the guess was wrong twice as the layout got deeper.
   */
  useEffect(() => {
    const el = paneRef.current
    if (el === null) return
    let raf = 0
    const refit = () => {
      cancelAnimationFrame(raf)
      raf = requestAnimationFrame(() => {
        if (el.clientWidth < 8 || el.clientHeight < 8) return
        const focusId = focusNodeIdRef.current ?? null
        const resolved = focusId === null
          ? null
          : (rf.getNode(focusId) !== undefined ? focusId : `band:${focusId}`)
        if (resolved !== null && rf.getNode(resolved) !== undefined) {
          void rf.fitView({ nodes: [{ id: resolved }], padding: 0.45, maxZoom: FOCUS_ZOOM, duration: 160 })
          return
        }
        void rf.fitView({ ...fitOptions(insetRightPx, el.clientWidth), duration: 160 })
      })
    }
    refit()
    const ro = new ResizeObserver(refit)
    ro.observe(el)
    return () => {
      cancelAnimationFrame(raf)
      ro.disconnect()
    }
    /* `insetRightPx` is a dependency because it changes what "fits" MEANS: the reserved strip on the right
       is part of the fit, so a panel opening or resize has to refit rather than leave the drawing under it. */
  }, [rf, fitKey, insetRightPx, focusNodeId])

  /**
   * Zoom compensation, written to CSS custom properties rather than React state.
   *
   * This fires on every wheel event. Through state it would re-render every node and edge mid-
   * gesture; as a style property on one element it is a single mutation the compositor handles.
   * Borrowed from n8n, which uses exactly this to keep edge strokes and labels from vanishing as
   * the graph shrinks.
   */
  const applyZoomVars = useCallback((zoom: number) => {
    const el = paneRef.current
    if (el === null) return
    el.style.setProperty('--zoom-comp', chromeCompensation(zoom).toFixed(3))
    el.style.setProperty('--edge-boost', edgeContrastBoost(zoom).toFixed(3))
  }, [])

  useEffect(() => {
    applyZoomVars(rf.getZoom())
  }, [applyZoomVars, rf, fitKey])

  const handleMove = useCallback(
    (_: unknown, viewport: { zoom: number }) => applyZoomVars(viewport.zoom),
    [applyZoomVars],
  )

  return (
    <div className="pstage-pane" ref={paneRef} data-detail={detail}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={NODE_TYPES}
        fitView
        fitViewOptions={fitOptions(insetRightPx, paneRef.current?.clientWidth ?? 0)}
        minZoom={MIN_ZOOM}
        maxZoom={MAX_ZOOM}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={interactive}
        panOnDrag={interactive}
        zoomOnScroll={interactive}
        zoomOnPinch={interactive}
        zoomOnDoubleClick={false}
        preventScrolling={interactive}
        proOptions={{ hideAttribution: true }}
        onMove={handleMove}
        onPaneClick={() => onSelect?.(null, false)}
      >
        <Background variant={BackgroundVariant.Dots} gap={26} size={1} />
        {interactive ? <Controls showInteractive={false} /> : null}
      </ReactFlow>
    </div>
  )
}

export function Stage(props: {
  /** Pixel width of the right-hand panel overlay, or null when none is open. */
  insetRightPx?: number | null
  scene: Scene
  detail: Detail
  /** Needed for edge routing: which axis the ranks advance along swaps with it. */
  direction: Direction
  selectedId: string | null
  /** See `Inner`: the entity under the pointer, from either pane. */
  hoveredId?: string | null
  /** Ids the composer is carrying. Marked on the card so a list built by clicking can be checked. */
  referencedIds?: readonly string[]
  onSelect?: (id: string | null, additive: boolean) => void
  onHover?: (id: string | null) => void
  /** "enumerate every execution of this StepType" — raised by the segmented bar. */
  onInspect?: (id: string) => void
  /** Rendered as a node at the diagram's top-left, so it zooms and pans with the drawing. */
  legend?: () => JSX.Element
  /** Publishes fit and zoom so a keyboard shortcut has something to drive. */
  /** False when the selection was made for the reader, so the graph is not faded for them. */
  dimUnrelated?: boolean
  /** When set, fit targets this node at reading zoom instead of the whole graph. */
  focusNodeId?: string | null
  handleRef?: Ref<CanvasViewportHandle>
  /** A click inside a group band's own space, outside any card. `additive` is Cmd (macOS) or Ctrl. */
  onSelectGroup?: (id: string | null, additive: boolean) => void
  selectedGroupId?: string | null
  interactive?: boolean
  fitKey: string
}): JSX.Element {
  const { interactive = true, ...rest } = props
  return (
    <div className="pstage">
      <ReactFlowProvider>
        <Inner {...rest} interactive={interactive} />
      </ReactFlowProvider>
    </div>
  )
}
