// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { NodeBox } from './NodeBox'

describe('NodeBox', () => {
  it('renders the semantic Connector icon only for Connector Steps', () => {
    const connector = renderToStaticMarkup(
      <NodeBox
        box={{ id: 'connector', x: 0, y: 0, w: 220, h: 52, kind: 'step', title: 'Generate', icon: 'connector' }}
        selected={false}
        hovered={false}
        referenced={false}
      />,
    )
    const ordinary = renderToStaticMarkup(
      <NodeBox
        box={{ id: 'ordinary', x: 0, y: 0, w: 220, h: 52, kind: 'step', title: 'Review' }}
        selected={false}
        hovered={false}
        referenced={false}
      />,
    )

    expect(connector).toContain('aria-label="Connector Step"')
    expect(connector).toContain('pbox-connector-icon')
    expect(ordinary).not.toContain('pbox-connector-icon')
  })

  it('renders an RPC card as a card and hands its role to the stylesheet', () => {
    const action = renderToStaticMarkup(
      <NodeBox
        box={{
          id: 'rpc:RemoveEntry',
          x: 0,
          y: 0,
          w: 280,
          h: 100,
          kind: 'rpcCard',
          title: 'RemoveEntry',
          subtitle: 'Action: Remove entry',
          rpcRole: 'action',
          rows: [
            { glyph: '◈', text: 'requires entries.manage', tone: 'quiet' },
            { glyph: '▪', text: 'writes entries', trail: '+1 more', tone: 'quiet' },
          ],
        }}
        selected={false}
      />,
    )

    expect(action).toContain('pbox pbox-rpcCard')
    expect(action).toContain('data-rpc-role="action"')
    expect(action).toContain('requires entries.manage')
    expect(action).toContain('<span class="pbox-text">writes entries</span><span class="pbox-trail">+1 more</span>')
    expect(action).not.toContain('pbox-inner')
  })
})
