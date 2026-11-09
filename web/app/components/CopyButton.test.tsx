// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Link } from 'lucide-react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CopyButton } from './CopyButton';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function stubClipboard(writeText: (text: string) => Promise<void>) {
  vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } });
}

describe('CopyButton', () => {
  it('copies a value read at click time and announces success', async () => {
    const writeText = vi.fn(async () => {});
    stubClipboard(writeText);
    let url = 'https://dex.test/v1/flows/a';
    render(
      <CopyButton className="button quiet" icon={Link} label="Copy link" value={() => url}>Copy link</CopyButton>,
    );
    url = 'https://dex.test/v1/flows/b';

    const button = screen.getByRole('button', { name: 'Copy link' });
    fireEvent.click(button);

    await waitFor(() => expect(button.textContent).toContain('Copied'));
    expect(writeText).toHaveBeenCalledWith('https://dex.test/v1/flows/b');
    expect(button.className).toContain('button quiet');
    expect(button.className).toContain('is-copied');
    expect(button.querySelector('[aria-live="polite"]')?.textContent).toBe('Copied');
  });

  it('shows and announces a failed clipboard write', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    stubClipboard(async () => {
      throw new Error('denied');
    });
    render(<CopyButton label="Copy run ID" value="run-1">Run ID</CopyButton>);

    const button = screen.getByRole('button', { name: 'Copy run ID' });
    fireEvent.click(button);

    await waitFor(() => expect(button.className).toContain('is-copy-failed'));
    expect(button.className).not.toContain('is-copied');
    expect(button.querySelector('[aria-live="polite"]')?.textContent).toBe('Copy failed');
  });
});
