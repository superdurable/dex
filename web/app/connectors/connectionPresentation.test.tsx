// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import {
  ConnectionNextStep,
  ConnectorAvatar,
  CredentialExpiry,
  connectorInitial,
  credentialExpiryLead,
  relativeTimeText,
} from './connectionPresentation';

const now = new Date('2026-11-13T12:00:00Z');

describe('Connector presentation helpers', () => {
  it('words a token expiry relative to now in both directions', () => {
    expect(relativeTimeText('2026-11-13T09:30:00Z', now)).toBe('3 hours ago');
    expect(relativeTimeText('2026-11-16T12:00:00Z', now)).toBe('in 3 days');
    expect(relativeTimeText('2026-11-14T12:00:00Z', now)).toBe('tomorrow');
    expect(relativeTimeText('2026-11-13T11:59:40Z', now)).toBe('now');
    expect(relativeTimeText('2026-12-20T12:00:00Z', now)).toBe('next month');
    expect(relativeTimeText('not a time', now)).toBe('not a time');
  });

  it('leads with expired or expires from the time, not from the status', () => {
    expect(credentialExpiryLead('2026-11-13T11:00:00Z', now)).toBe('Token expired');
    expect(credentialExpiryLead('2026-11-13T13:00:00Z', now)).toBe('Token expires');
  });

  it('shows the relative expiry and keeps the exact time on hover', () => {
    const markup = renderToStaticMarkup(<CredentialExpiry iso="2026-11-13T19:35:26.079571Z" now={now} />);
    expect(markup).toMatch(/^<span class="connector-expiry">Token expires <time dateTime="2026-11-13T19:35:26.079571Z" title="[^"]*2026[^"]*">in 8 hours<\/time><\/span>$/);
  });

  it('draws a brand-neutral initial that screen readers skip', () => {
    expect(connectorInitial('Gmail')).toBe('G');
    expect(connectorInitial('  3scale')).toBe('3');
    expect(connectorInitial('élan')).toBe('É');
    expect(connectorInitial('—')).toBe('?');
    expect(renderToStaticMarkup(<ConnectorAvatar name="Slack" size="lg" />))
      .toBe('<span aria-hidden="true" class="connector-avatar" data-size="lg">S</span>');
  });
});

describe('Connection next step', () => {
  const render = (status: Parameters<typeof ConnectionNextStep>[0]['status'], credentialExpiresAt?: string) => renderToStaticMarkup(
    <ConnectionNextStep connectorName="Gmail" credentialExpiresAt={credentialExpiresAt} now={now} onAct={() => undefined} status={status} />,
  );

  it('asks an expired connection to reauthorize and says how long ago it expired', () => {
    const markup = render('Expired', '2026-11-13T02:00:00Z');
    expect(markup).toContain('data-tone="expired"');
    expect(markup).toContain('Token expired <time');
    expect(markup).toContain('>10 hours ago</time>');
    expect(markup).toContain('Gmail calls fail until you authorize this connection again.');
    expect(markup).toMatch(/<button class="v2-primary connector-next-action" type="button">Reauthorize<svg/);
    expect(render('Expired')).toContain('Authorization expired');
  });

  it('asks a missing connection to set up and gives a Ready connection nothing to do', () => {
    const missing = render('Missing');
    expect(missing).toContain('data-tone="missing"');
    expect(missing).toContain('Not set up yet');
    expect(missing).toMatch(/type="button">Set up<svg/);
    expect(render('Ready')).toBe('');
  });

  it('explains conflicts and unsupported releases without an action', () => {
    for (const status of ['Conflict', 'Unsupported'] as const) {
      const markup = render(status);
      expect(markup).toContain(`data-tone="${status.toLowerCase()}"`);
      expect(markup).not.toContain('<button');
    }
  });
});
