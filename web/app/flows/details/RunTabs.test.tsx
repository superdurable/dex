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
import { Rows3, Waves } from 'lucide-react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { EventsViewSwitch, LiveStatus, RunInputOutputButtons, RunViewTabs, type RunView } from './RunTabs';

afterEach(cleanup);

const views: RunView<'timeline' | 'steps' | 'design' | 'stream' | 'actions'>[] = [
  { id: 'timeline', label: 'Events', Icon: Rows3, alsoSelectedFor: ['steps', 'design'] },
  { id: 'stream', label: 'Streams', Icon: Waves },
  { id: 'actions', label: 'Actions', Icon: Waves, isExperimental: true },
];

describe('run views', () => {
  it('puts every view on the tab row, with Events selected for Timeline, Graph and old Design links', () => {
    const onSelect = vi.fn();
    const { rerender } = render(<MemoryRouter><RunViewTabs onSelect={onSelect} shownView="steps" views={views} /></MemoryRouter>);
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['Events', 'Streams', 'ActionsExperimental']);
    expect(screen.getByRole('tab', { name: 'Events' }).getAttribute('aria-selected')).toBe('true');
    rerender(<MemoryRouter><RunViewTabs onSelect={onSelect} shownView="design" views={views} /></MemoryRouter>);
    expect(screen.getByRole('tab', { name: 'Events' }).getAttribute('aria-selected')).toBe('true');
    fireEvent.click(screen.getByRole('tab', { name: 'Streams' }));
    expect(onSelect).toHaveBeenCalledWith('stream');
  });

  it('switches Events between Timeline and Graph', () => {
    const onChange = vi.fn();
    render(<EventsViewSwitch onChange={onChange} view="timeline" />);
    expect(screen.getByRole('button', { name: 'Timeline' }).getAttribute('aria-pressed')).toBe('true');
    fireEvent.click(screen.getByRole('button', { name: 'Graph' }));
    expect(onChange).toHaveBeenCalledWith('graph');
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
