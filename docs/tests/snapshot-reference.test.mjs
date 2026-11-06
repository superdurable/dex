// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import assert from 'node:assert/strict';
import {cp, mkdir, mkdtemp, readFile, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import test from 'node:test';
import {
  droppedVersionRedirects, localizeAllSnapshotLinks, readReferenceDocuments, referenceVersion,
  retargetRedirects, rewriteSnapshotLinks, snapshotReference,
} from '../scripts/snapshot-reference.mjs';

test('release versions follow component boundaries', () => {
  assert.equal(referenceVersion('go', 'v1.35.2'), '1.35.x');
  assert.equal(referenceVersion('cli', '1.36.x'), '1.36.x');
  assert.equal(referenceVersion('server', 'v0.1.19'), '0.1.19');
  assert.equal(referenceVersion('server', '1.0.2'), '1.0.x');
  assert.equal(referenceVersion('server', '0.1.20'), '0.1.20');
  assert.equal(referenceVersion('server', '1.2.3'), '1.2.x');
  for (const version of ['0.1.20-smoke.7', '1.0.0-rc.1', '0.2.0', '1.2.3+build']) {
    assert.throws(() => referenceVersion('server', version), /Invalid server/);
  }
  for (const version of ['../bad', '1.2.0-beta.1', '0.1.x', '1.2', '1.2.3/evil']) {
    assert.throws(() => referenceVersion('go', version));
  }
});

test('pruning matches document IDs across slugs and falls back to the index in both locales', () => {
  const redirects = droppedVersionRedirects('web', '1.28.x', [
    {id: 'index', slug: '/'}, {id: 'page', slug: '/old-slug/'}, {id: 'gone', slug: '/removed/'},
  ], [{id: 'index', slug: '/'}, {id: 'page', slug: '/new-slug/'}]);
  assert.deepEqual(redirects.slice(0, 3), [
    {from: '/reference/web/1.28.x/', to: '/reference/web/'},
    {from: '/reference/web/1.28.x/old-slug/', to: '/reference/web/new-slug/'},
    {from: '/reference/web/1.28.x/removed/', to: '/reference/web/'},
  ]);
  assert.equal(redirects[4].to, '/zh-Hans/reference/web/new-slug/');
  assert.deepEqual(retargetRedirects([
    {from: '/legacy/', to: '/reference/web/1.28.x/old-slug/'},
  ], new Map(redirects.map(({from, to}) => [from, to])))[0],
  {from: '/legacy/', to: '/reference/web/new-slug/'});
  assert.throws(() => retargetRedirects([{from: '/a/', to: '/b/'}, {from: '/b/', to: '/a/'}], new Map()), /cycle/);
});

test('snapshot retains five releases, prunes files, flattens redirects, and leaves other instances alone', async () => {
  const siteDirectory = await mkdtemp(join(tmpdir(), 'dex-reference-snapshot-'));
  try {
    const latestDirectory = join(siteDirectory, 'reference/web');
    await mkdir(join(latestDirectory, '01-guide'), {recursive: true});
    await writeFile(join(latestDirectory, 'index.mdx'), '---\nslug: /\n---\n# Web');
    await writeFile(join(latestDirectory, '01-guide/02-worker.mdx'), '---\nid: worker\nslug: new-name\n---\n# Worker');
    await writeFile(join(latestDirectory, '01-guide/_partial.mdx'), '# Partial');
    await mkdir(join(latestDirectory, '_drafts'));
    await writeFile(join(latestDirectory, '_drafts/draft.mdx'), '# Draft');
    await writeFile(join(siteDirectory, 'reference/versions.json'), JSON.stringify({web: '1.35.x', go: '1.35.x'}));
    await writeFile(join(siteDirectory, 'redirects.json'), JSON.stringify([
      {from: '/legacy/', to: '/reference/web/1.28.x/guide/old-name'},
    ]));
    const previousVersions = ['1.35.x', '1.32.x', '1.30.x', '1.29.x', '1.28.x'];
    await writeFile(join(siteDirectory, 'reference-web_versions.json'), JSON.stringify(previousVersions));
    await writeFile(join(siteDirectory, 'reference-go_versions.json'), '["1.32.x"]\n');
    await mkdir(join(siteDirectory, 'reference-web_versioned_sidebars'));
    for (const version of previousVersions) {
      const directory = join(siteDirectory, `reference-web_versioned_docs/version-${version}`);
      await cp(latestDirectory, directory, {recursive: true});
      await writeFile(join(directory, '01-guide/02-worker.mdx'), '---\nid: worker\nslug: old-name\n---\n# Worker');
      await writeFile(join(directory, 'gone.mdx'), '# Gone');
      await writeFile(join(siteDirectory, `reference-web_versioned_sidebars/version-${version}-sidebars.json`), '{}');
    }
    let snapshotCount = 0;
    const createSnapshot = async (directory, component, version) => {
      snapshotCount += 1;
      assert.equal(component, 'web');
      await cp(latestDirectory, join(directory, `reference-web_versioned_docs/version-${version}`), {recursive: true});
      await writeFile(join(directory, `reference-web_versioned_sidebars/version-${version}-sidebars.json`), '{}');
    };
    const result = await snapshotReference('web', '1.36.0', {siteDirectory, createSnapshot});
    assert.deepEqual(result.retainedVersions, ['1.36.x', '1.35.x', '1.32.x', '1.30.x', '1.29.x']);
    assert.deepEqual(result.droppedVersions, ['1.28.x']);
    const redirects = JSON.parse(await readFile(join(siteDirectory, 'redirects.json'), 'utf8'));
    assert.deepEqual(redirects[0], {from: '/legacy/', to: '/reference/web/guide/new-name/'});
    assert.ok(redirects.some(({from, to}) => from === '/zh-Hans/reference/web/1.28.x/gone/' && to === '/zh-Hans/reference/web/'));
    await assert.rejects(readFile(join(siteDirectory, 'reference-web_versioned_docs/version-1.28.x/index.mdx')), {code: 'ENOENT'});
    await assert.rejects(readFile(join(siteDirectory, 'reference-web_versioned_sidebars/version-1.28.x-sidebars.json')), {code: 'ENOENT'});
    assert.deepEqual(JSON.parse(await readFile(join(siteDirectory, 'reference/versions.json'), 'utf8')), {web: '1.36.x', go: '1.35.x'});
    assert.equal(await readFile(join(siteDirectory, 'reference-go_versions.json'), 'utf8'), '["1.32.x"]\n');
    assert.deepEqual(await readReferenceDocuments(latestDirectory), [
      {id: 'guide/worker', slug: '/guide/new-name/'}, {id: 'index', slug: '/'},
    ]);
    await writeFile(join(latestDirectory, 'patched.mdx'), '# Patched');
    const patchResult = await snapshotReference('web', '1.36.1', {siteDirectory, createSnapshot});
    assert.equal(snapshotCount, 2, 'a patch release must refresh the hidden latest snapshot');
    assert.deepEqual(patchResult.retainedVersions, ['1.36.x', '1.35.x', '1.32.x', '1.30.x', '1.29.x']);
    assert.deepEqual(patchResult.droppedVersions, []);
    assert.equal(await readFile(join(siteDirectory, 'reference-web_versioned_docs/version-1.36.x/patched.mdx'), 'utf8'), '# Patched');
    assert.deepEqual(JSON.parse(await readFile(join(siteDirectory, 'reference-web_versions.json'), 'utf8')),
      ['1.36.x', '1.35.x', '1.32.x', '1.30.x', '1.29.x']);
    await assert.rejects(snapshotReference('web', '1.32.0', {siteDirectory, createSnapshot}), /older/);
    await assert.rejects(snapshotReference('../invalid', '1.36.0', {siteDirectory, createSnapshot}), /Unknown/);
    await assert.rejects(snapshotReference('go', '1.36.0', {siteDirectory, createSnapshot}),
      /hidden snapshot of the latest 1\.35\.x is missing.*snapshot-reference\.mjs go 1\.35\.x/);
    assert.equal(snapshotCount, 2, 'a missing hidden latest snapshot must stop before snapshotting');
  } finally {
    await rm(siteDirectory, {recursive: true, force: true});
  }
});

test('bootstrap snapshot of the latest label stays hidden and enables the next release', async () => {
  const siteDirectory = await mkdtemp(join(tmpdir(), 'dex-reference-bootstrap-'));
  try {
    await mkdir(join(siteDirectory, 'reference/server'), {recursive: true});
    await writeFile(join(siteDirectory, 'reference/server/index.mdx'), '---\nslug: /\n---\n# Server');
    await writeFile(join(siteDirectory, 'reference/versions.json'), JSON.stringify({server: '0.1.19'}));
    await writeFile(join(siteDirectory, 'redirects.json'), '[]');
    const createSnapshot = async (directory, component, version) => {
      await cp(join(directory, 'reference', component), join(directory, `reference-${component}_versioned_docs/version-${version}`), {recursive: true});
      await mkdir(join(directory, `reference-${component}_versioned_sidebars`), {recursive: true});
      await writeFile(join(directory, `reference-${component}_versioned_sidebars/version-${version}-sidebars.json`), '{}');
    };
    await assert.rejects(snapshotReference('server', '0.1.20', {siteDirectory, createSnapshot}), /0\.1\.19 is missing/);
    assert.deepEqual((await snapshotReference('server', 'v0.1.19', {siteDirectory, createSnapshot})).retainedVersions, ['0.1.19']);
    assert.deepEqual((await snapshotReference('server', '0.1.20', {siteDirectory, createSnapshot})).retainedVersions, ['0.1.20', '0.1.19']);
    assert.deepEqual(JSON.parse(await readFile(join(siteDirectory, 'reference/versions.json'), 'utf8')), {server: '0.1.20'});
  } finally {
    await rm(siteDirectory, {recursive: true, force: true});
  }
});

test('snapshots link to their own component at the same version, including the hidden latest snapshot', async () => {
  const siteDirectory = await mkdtemp(join(tmpdir(), 'dex-reference-local-links-'));
  try {
    const currentDirectory = join(siteDirectory, 'reference/server');
    await mkdir(currentDirectory, {recursive: true});
    const currentContent = [
      '[w](/reference/server/workers) [i](/reference/server/) [bare](/reference/server)',
      '[anchor](/reference/server/configuration#limits) [slash](/reference/server/search/#monitoring)',
      '<Link to="/reference/server/upgrades/">u</Link> [ref]: /reference/server/metrics',
      '[angle](</reference/server/workers/#queue>) [angle-ref]: </reference/server/search>',
      '[web](/reference/web/dex-web) [other](/reference/serverless/x) [api](pathname:///api/go/1.35.x/)',
      '[ext](https://docs.superdurable.io/reference/server/workers) [hub](/reference/) [zh](/zh-Hans/reference/server/workers)',
      '[pinned](/reference/server/0.1.18/workers) [guide](/primitives/flow)',
    ].join('\n');
    await writeFile(join(currentDirectory, 'index.mdx'), currentContent);
    await writeFile(join(siteDirectory, 'reference/versions.json'), JSON.stringify({server: '0.1.19', web: '1.35.x'}));
    await writeFile(join(siteDirectory, 'redirects.json'), '[]');
    const createSnapshot = async (directory, component, version) => {
      await cp(join(directory, 'reference', component), join(directory, `reference-${component}_versioned_docs/version-${version}`), {recursive: true});
      await mkdir(join(directory, `reference-${component}_versioned_sidebars`), {recursive: true});
      await writeFile(join(directory, `reference-${component}_versioned_sidebars/version-${version}-sidebars.json`),
        '{"reference": [{"type": "link", "href": "/reference/"}, {"type": "link", "href": "/reference/server/workers"}]}');
    };
    await snapshotReference('server', '0.1.19', {siteDirectory, createSnapshot});
    const hiddenSnapshot = join(siteDirectory, 'reference-server_versioned_docs/version-0.1.19/index.mdx');
    assert.equal(await readFile(hiddenSnapshot, 'utf8'), [
      '[w](/reference/server/0.1.19/workers) [i](/reference/server/0.1.19/) [bare](/reference/server/0.1.19)',
      '[anchor](/reference/server/0.1.19/configuration#limits) [slash](/reference/server/0.1.19/search/#monitoring)',
      '<Link to="/reference/server/0.1.19/upgrades/">u</Link> [ref]: /reference/server/0.1.19/metrics',
      '[angle](</reference/server/0.1.19/workers/#queue>) [angle-ref]: </reference/server/0.1.19/search>',
      '[web](/reference/web/dex-web) [other](/reference/serverless/x) [api](pathname:///api/go/1.35.x/)',
      '[ext](https://docs.superdurable.io/reference/server/workers) [hub](/reference/) [zh](/zh-Hans/reference/server/workers)',
      '[pinned](/reference/server/0.1.18/workers) [guide](/primitives/flow)',
    ].join('\n'));
    assert.equal(await readFile(join(siteDirectory, 'reference-server_versioned_sidebars/version-0.1.19-sidebars.json'), 'utf8'),
      '{"reference": [{"type": "link", "href": "/reference/"}, {"type": "link", "href": "/reference/server/0.1.19/workers"}]}');
    assert.equal(await readFile(join(currentDirectory, 'index.mdx'), 'utf8'), currentContent);
    await snapshotReference('server', '0.1.20', {siteDirectory, createSnapshot});
    assert.match(await readFile(join(siteDirectory, 'reference-server_versioned_docs/version-0.1.20/index.mdx'), 'utf8'),
      /^\[w\]\(\/reference\/server\/0\.1\.20\/workers\)/);
    assert.match(await readFile(hiddenSnapshot, 'utf8'), /^\[w\]\(\/reference\/server\/0\.1\.19\/workers\)/);
    assert.deepEqual(await localizeAllSnapshotLinks({siteDirectory}), {server: 0, web: 0});
  } finally {
    await rm(siteDirectory, {recursive: true, force: true});
  }
});

test('link rewrites update whole paths in retained snapshots only', async () => {
  const siteDirectory = await mkdtemp(join(tmpdir(), 'dex-reference-links-'));
  try {
    const snapshot = join(siteDirectory, 'reference-go_versioned_docs/version-1.32.x');
    await mkdir(snapshot, {recursive: true});
    await mkdir(join(siteDirectory, 'reference-go_versioned_sidebars'));
    await mkdir(join(siteDirectory, 'reference/go'), {recursive: true});
    await writeFile(join(snapshot, 'index.mdx'), [
      '[a](/primitives/old) [b](/primitives/old/#retry) <Link to="/primitives/old/">c</Link>',
      '[keep](/primitives/older) [keep](/x/primitives/old) [ref]: /primitives/old',
      '[angle](</primitives/old>) [anchored](</primitives/old/#section>) </primitives/older>',
      '[angle-ref]: </primitives/old>',
    ].join('\n'));
    await writeFile(join(siteDirectory, 'reference-go_versioned_sidebars/version-1.32.x-sidebars.json'),
      '{"docs": [{"type": "link", "href": "/primitives/old"}]}');
    await writeFile(join(siteDirectory, 'reference/go/index.mdx'), '[a](/primitives/old)');
    const changedFiles = await rewriteSnapshotLinks('/primitives/old/', '/primitives/new', {siteDirectory});
    assert.deepEqual(changedFiles, [
      'reference-go_versioned_docs/version-1.32.x/index.mdx',
      'reference-go_versioned_sidebars/version-1.32.x-sidebars.json',
    ]);
    assert.equal(await readFile(join(snapshot, 'index.mdx'), 'utf8'), [
      '[a](/primitives/new) [b](/primitives/new/#retry) <Link to="/primitives/new/">c</Link>',
      '[keep](/primitives/older) [keep](/x/primitives/old) [ref]: /primitives/new',
      '[angle](</primitives/new>) [anchored](</primitives/new/#section>) </primitives/older>',
      '[angle-ref]: </primitives/new>',
    ].join('\n'));
    assert.equal(await readFile(join(siteDirectory, 'reference/go/index.mdx'), 'utf8'), '[a](/primitives/old)');
    await assert.rejects(rewriteSnapshotLinks('/a#b', '/c', {siteDirectory}), /without anchors/);
  } finally {
    await rm(siteDirectory, {recursive: true, force: true});
  }
});
