// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Rows3, Waves, Workflow } from 'lucide-react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { LiveStatus, RunInputOutputButtons, RunViewTabs, type RunView } from './RunTabs';

afterEach(cleanup);

const views: RunView<'timeline' | 'steps' | 'design' | 'stream' | 'actions'>[] = [
  { id: 'timeline', label: 'Timeline', Icon: Rows3 },
  { id: 'steps', label: 'Graph', Icon: Workflow, alsoSelectedFor: ['design'] },
  { id: 'stream', label: 'Streams', Icon: Waves, moreGroup: 'Advanced' },
  { id: 'actions', label: 'Actions', Icon: Waves, moreGroup: 'Experimental', isExperimental: true },
];

describe('run views', () => {
  it('keeps Graph selected for its Design view and groups the rest under More', () => {
    const onSelect = vi.fn();
    render(<MemoryRouter><RunViewTabs onSelect={onSelect} shownView="design" views={views} /></MemoryRouter>);
    expect(screen.getByRole('tab', { name: 'Graph' }).getAttribute('aria-selected')).toBe('true');
    fireEvent.click(screen.getByRole('tab', { name: /^More/ }));
    expect(screen.getByRole('group', { name: 'Advanced' }).textContent).toContain('Streams');
    expect(screen.getByRole('group', { name: 'Experimental' }).textContent).toContain('ActionsExperimental');
    fireEvent.click(screen.getByRole('menuitem', { name: 'Streams' }));
    expect(onSelect).toHaveBeenCalledWith('stream');
  });

  it('names the More view that is open', () => {
    render(<MemoryRouter><RunViewTabs onSelect={vi.fn()} shownView="stream" views={views} /></MemoryRouter>);
    const more = screen.getByRole('tab', { name: /^Streams/ });
    expect(more.getAttribute('aria-selected')).toBe('true');
  });
});

describe('Live status', () => {
  it('pauses from Live and resumes from paused', () => {
    const onChange = vi.fn();
    const { rerender } = render(<LiveStatus isLive onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: /^Live: / }));
    expect(onChange).toHaveBeenLastCalledWith(false);
    rerender(<LiveStatus isLive={false} onChange={onChange} />);
    expect(screen.getByRole('button', { name: /^Updates paused/ }).textContent).toContain('Resume');
    fireEvent.click(screen.getByRole('button', { name: /^Updates paused/ }));
    expect(onChange).toHaveBeenLastCalledWith(true);
  });
});

describe('Input and Result', () => {
  it('shows the start and close events, and marks the one selected', () => {
    const onShowEvent = vi.fn();
    render(<RunInputOutputButtons closeEventId={14} onShowEvent={onShowEvent} selectedEventId={14} startEventId={1} />);
    fireEvent.click(screen.getByRole('button', { name: 'Input' }));
    expect(onShowEvent).toHaveBeenLastCalledWith(1);
    expect(screen.getByRole('button', { name: 'Result' }).getAttribute('aria-pressed')).toBe('true');
  });

  it('offers only Input while the run is open', () => {
    render(<RunInputOutputButtons closeEventId={null} onShowEvent={vi.fn()} selectedEventId={null} startEventId={1} />);
    expect(screen.queryByRole('button', { name: 'Result' })).toBeNull();
  });
});
