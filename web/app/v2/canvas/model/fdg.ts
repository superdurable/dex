// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * Flow Definition Graph v1 — the wire format emitted by `dexcli visualize --json`.
 *
 * These types mirror the published schema, with one deliberate loosening: `kind`,
 * `wait.type` and `decision.type` are declared as OPEN strings rather than unions.
 * That is not laziness — the real schema declares them as bare `string`, and the
 * documented `wait.type` enum does not contain `'failure'` even though the reference
 * corpus does. A closed union here would reject real input.
 *
 * Nothing in this file is the domain model. It is the shape of somebody else's JSON.
 */

export interface SourceSpan {
  startLine: number
  startColumn: number
  endLine: number
  endColumn: number
}

/** Vocabulary observed across the reference corpus. Not exhaustive, not enforced. */
export const KNOWN_NODE_KINDS = [
  'step',
  'rpc',
  'timeout_handler',
  'wait',
  'wait_dispatch',
  'decision',
  'decision_dispatch',
  'attribute',
  'channel',
  'stream',
  'subflow',
  'unknown',
] as const

/** The one genuinely closed union in the node type. */
export type FdgConditionKind = 'channel' | 'timer' | 'subflow' | 'unknown'

export interface FdgCondition {
  kind: FdgConditionKind
  label: string
  resourceId?: string
  subFlowId?: string
  expression?: string
  span?: SourceSpan
}

export interface FdgNode {
  id: string
  kind: string
  name: string
  parentId?: string
  condition?: string
  /** 'execute' | 'wait_for' | 'wait_for+execute' | 'rpc' | 'timeout' — a `+`-joined compound, not an enum. */
  phase?: string
  start?: boolean
  external?: boolean
  span?: SourceSpan
  resource?: { valueType: string; map?: boolean }
  wait?: { type: string; conditions: FdgCondition[] }
  decision?: {
    type: string
    checkedChannels?: string[]
    cancellations?: { stepId: string; scope: 'all' | 'siblings' }[]
  }
  metadata?: Record<string, unknown>
}

export interface FdgEdge {
  id: string
  kind: string
  from: string
  to: string
  label?: string
  condition?: string
  /** A string, not a number. The corpus only ever contains the literal "×N". */
  multiplicity?: string
  span?: SourceSpan
  metadata?: Record<string, unknown>
}

export interface FdgDiagnostic {
  severity: 'warning' | 'error'
  code: string
  message: string
  span?: SourceSpan
}

export interface FlowDefinitionGraph {
  schemaVersion: string
  valid: boolean
  source: { language: string; path: string }
  flow: { name: string; startStepId?: string; span?: SourceSpan }
  nodes: FdgNode[]
  edges: FdgEdge[]
  diagnostics: FdgDiagnostic[]
}
