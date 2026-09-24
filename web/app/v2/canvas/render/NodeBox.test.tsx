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
})
