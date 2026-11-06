// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import assert from 'node:assert/strict';
import {retiredApiReferenceRedirectTarget} from '../src/apiReferenceRedirect.mjs';

const releasesByLanguage = {go: ['1.35.0', '1.32.0']};
const publishedPaths = new Set(['/api/go/latest/dex/']);
const checkedPaths = [];
const isPagePublished = async (pathname) => {
  checkedPaths.push(pathname);
  return publishedPaths.has(pathname);
};
const redirectTarget = (pathname, search = '', hash = '') =>
  retiredApiReferenceRedirectTarget({pathname, search, hash}, releasesByLanguage, isPagePublished);

assert.equal(await redirectTarget('/api/go/1.26/dex/', '?tab=1', '#constants'), '/api/go/latest/dex/?tab=1#constants',
  'a retired page that exists in latest keeps its query and fragment');
assert.equal(await redirectTarget('/api/go/1.26/missing/', '?tab=1', '#constants'), '/reference/go/?tab=1#constants',
  'the reference index fallback keeps the query and fragment');
assert.deepEqual(checkedPaths, ['/api/go/latest/dex/', '/api/go/latest/missing/']);
for (const pathname of ['/api/go/1.32/missing/', '/api/go/latest/missing/', '/api/rust/1.0/x', '/reference/go/1.26/',
  '/api/constructor/1.0/x', '/api/toString/1.0/x', '/api/__proto__/1.0/x', '/api/hasOwnProperty/1.0/x']) {
  assert.equal(await redirectTarget(pathname), undefined, `${pathname} must not redirect`);
}
assert.equal(checkedPaths.length, 2, 'non-retired URLs must not probe the site');
console.log('API reference 404 redirect test passed for retired, current, latest, unknown, and inherited-property URLs.');
