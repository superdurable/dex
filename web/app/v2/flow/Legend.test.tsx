// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { safeDecode } from '../canvas/model/decode'
import type { Box } from '../canvas/views/types'
import { Legend } from './Legend'

describe('Legend', () => {
  it('decodes the RPC card, Action, and view marks when the scene draws them', () => {
    const markup = legendFor([card('rpc:RemoveEntry', 'action'), card('rpc:AddEntry'), card('rpc:GetDexSummary', 'view')])

    expect(marksIn(markup)).toEqual([
      ['rpc-card', 'an RPC and the state it reads and writes'],
      ['action', 'an Action RPC operators can run'],
      ['view', 'a Summary or Display view RPC'],
    ])
  })

  it('omits the mark of an RPC card kind the scene does not draw', () => {
    const markup = legendFor([card('rpc:RemoveEntry', 'action'), card('rpc:GetDexDisplay', 'view')])

    expect(marksIn(markup).map(([mark]) => mark)).toEqual(['action', 'view'])
  })
})

function legendFor(boxes: Box[]): string {
  const flow = safeDecode(
    {
      schemaVersion: '2.0',
      valid: true,
      source: { language: 'go', path: 'entity_list_flow.go' },
      flow: { name: 'EntityListFlow' },
      nodes: [],
      edges: [],
      diagnostics: [],
    },
    { generated: true, note: 'test' },
  )
  return renderToStaticMarkup(
    <Legend scene={{ boxes, links: [], bands: [], width: 640, height: 200, notes: [] }} flow={flow} />,
  )
}

function card(id: string, rpcRole?: Box['rpcRole']): Box {
  return {
    id,
    x: 0,
    y: 0,
    w: 280,
    h: 62,
    kind: 'rpcCard',
    title: id.replace(/^rpc:/, ''),
    ...(rpcRole === undefined ? {} : { rpcRole }),
  }
}

function marksIn(markup: string): [string, string][] {
  return [...markup.matchAll(/data-mark="([^"]+)"><\/span><span class="plegend-text">([^<]+)</g)].map(
    ([, mark, label]) => [mark, label],
  )
}
