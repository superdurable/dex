// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { ActionInput } from './SelectedRunPanel';

describe('ActionInput', () => {
  it('keeps ordinary Action strings as text-only inputs', () => {
    const markup = renderToStaticMarkup(
      <ActionInput
        field={{
          fieldName: 'reference',
          valueType: 'string',
          source: 'user',
          required: true,
          description: 'Reference',
        }}
        value="typed-value"
        onChange={() => undefined}
        onScanRequested={() => undefined}
      />,
    );

    expect(markup).toContain('value="typed-value"');
    expect(markup).not.toContain('Scan QR code');
  });

  it('adds QR capture beside the unchanged text input', () => {
    const markup = renderToStaticMarkup(
      <ActionInput
        field={{
          fieldName: 'reference',
          valueType: 'string',
          source: 'user',
          capture: 'qr-code',
          required: true,
          description: 'Reference',
        }}
        value="manual-value"
        onChange={() => undefined}
        onScanRequested={() => undefined}
      />,
    );

    expect(markup).toContain('type="text"');
    expect(markup).toContain('value="manual-value"');
    expect(markup).toContain('type="button">Scan QR code</button>');
  });
});
