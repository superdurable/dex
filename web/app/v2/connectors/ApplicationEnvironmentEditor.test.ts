// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { ApplicationEnvironmentForm, applicationEnvironmentWrite, type ApplicationEnvironmentSnapshot } from './ApplicationEnvironmentEditor';

const snapshot: ApplicationEnvironmentSnapshot = {
  appManifestRevision: 3, configurationRevision: 7, csrfToken: 'fixture-csrf', obsolete: ['PREVIOUS_SETTING'],
  fields: [
    { name: 'APP_ENV', required: true, secret: false, minLength: 0, enum: ['production'], configured: true, value: 'production' },
    { name: 'SIGNING_SECRET', required: true, secret: true, minLength: 32, enum: [], configured: true },
  ],
};

describe('Application environment editor', () => {
  it('keeps untouched secrets out of writes and distinguishes explicit empty strings', () => {
    expect(applicationEnvironmentWrite(snapshot, { APP_ENV: '' }, [])).toEqual({ values: { APP_ENV: '' }, secrets: {}, remove: [] });
    expect(applicationEnvironmentWrite(snapshot, { SIGNING_SECRET: 'new-private' }, [])).toEqual({ values: {}, secrets: { SIGNING_SECRET: 'new-private' }, remove: [] });
    expect(applicationEnvironmentWrite(snapshot, { SIGNING_SECRET: '' }, ['SIGNING_SECRET'])).toEqual({ values: {}, secrets: {}, remove: ['SIGNING_SECRET'] });
    expect(applicationEnvironmentWrite(snapshot, { AWS_REGION: 'untrusted' }, [])).toEqual({ values: {}, secrets: {}, remove: [] });
  });
  it('renders declared environment before Release without exposing private values or references', () => {
    const markup = renderToStaticMarkup(createElement(ApplicationEnvironmentForm, { snapshot, onSave: async () => {} }));
    expect(markup).toContain('Application environment');
    expect(markup).toContain('APP_ENV');
    expect(markup).toContain('value="production"');
    expect(markup).toContain('type="password"');
    expect(markup).toContain('Configured; leave untouched to keep');
    expect(markup).toContain('At least 32 characters.');
    expect(markup).toContain('Remove PREVIOUS_SETTING');
    expect(markup).not.toContain('secretRef');
    expect(markup).not.toContain('sha256:');
  });
  it('supports applications that declare no settings', () => {
    const markup = renderToStaticMarkup(createElement(ApplicationEnvironmentForm, { snapshot: { ...snapshot, fields: [], obsolete: [] }, onSave: async () => {} }));
    expect(markup).toContain('declares no environment settings');
    expect(markup).toContain('disabled=""');
  });
});
