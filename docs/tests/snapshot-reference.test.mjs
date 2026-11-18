// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import assert from 'node:assert/strict';
import {cp, mkdir, mkdtemp, readdir, readFile, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import test from 'node:test';
import {Globby} from '@docusaurus/utils';
import {
  compareVersions, localizeAllSnapshotLinks, pinServerCompatibilityVersion, readReferenceDocuments,
  referenceVersion, rewriteSnapshotLinks, snapshotReference,
} from '../scripts/snapshot-reference.mjs';

const docsDirectory = fileURLToPath(new URL('../', import.meta.url));

test('a server snapshot pins its compatibility section to its own version', async () => {
  const siteDirectory = await mkdtemp(join(tmpdir(), 'dex-reference-compatibility-'));
  try {
    const serverDirectory = join(siteDirectory, 'reference-server_versioned_docs/version-0.1.32');
    const webDirectory = join(siteDirectory, 'reference-web_versioned_docs/version-1.45.x');
    await mkdir(serverDirectory, {recursive: true});
    await mkdir(webDirectory, {recursive: true});
    await writeFile(join(serverDirectory, 'upgrades.mdx'), '# Upgrades\n\n<ServerZdCompatibility />\n');
    await writeFile(join(serverDirectory, 'workers.mdx'), '# Workers\n');
    await writeFile(join(webDirectory, 'index.mdx'), '<ServerZdCompatibility />\n');

    assert.deepEqual(await pinServerCompatibilityVersion('server', '0.1.32', {siteDirectory}),
      ['reference-server_versioned_docs/version-0.1.32/upgrades.mdx']);
    assert.equal(await readFile(join(serverDirectory, 'upgrades.mdx'), 'utf8'),
      '# Upgrades\n\n<ServerZdCompatibility version="0.1.32" />\n');
    assert.deepEqual(await pinServerCompatibilityVersion('server', '0.1.32', {siteDirectory}), []);
    assert.deepEqual(await pinServerCompatibilityVersion('web', '1.45.x', {siteDirectory}), []);
    assert.equal(await readFile(join(webDirectory, 'index.mdx'), 'utf8'), '<ServerZdCompatibility />\n');
  } finally {
    await rm(siteDirectory, {recursive: true, force: true});
  }
});

test('release versions follow component boundaries', () => {
  assert.equal(referenceVersion('go', 'v1.35.2'), '1.35.x');
  assert.equal(referenceVersion('cli', '1.36.x'), '1.36.x');
  assert.equal(referenceVersion('server', 'v0.1.19'), '0.1.19');
  assert.equal(referenceVersion('server', '1.0.2'), '1.0.x');
  assert.equal(referenceVersion('server', '0.1.20'), '0.1.20');
  assert.equal(referenceVersion('server', '1.2.3'), '1.2.x');
  assert.equal(referenceVersion('agents', 'v0.4.0'), '0.4.x');
  assert.equal(referenceVersion('agents', '1.2.3'), '1.2.x');
  for (const version of ['0.1.20-smoke.7', '1.0.0-rc.1', '0.2.0', '1.2.3+build']) {
    assert.throws(() => referenceVersion('server', version), /Invalid server/);
  }
  for (const version of ['../bad', '1.2.0-beta.1', '0.1.x', '1.2', '1.2.3/evil']) {
    assert.throws(() => referenceVersion('go', version));
  }
  for (const version of ['0.5.0-rc.1', '0.5', '../0.5.0']) {
    assert.throws(() => referenceVersion('agents', version));
  }
  assert.equal(referenceVersion('dex-app', 'v1'), 'v1');
  assert.equal(referenceVersion('dex-app', '2'), 'v2');
  for (const version of ['v0', '1.0.0', 'v1.1', '../v1']) {
    assert.throws(() => referenceVersion('dex-app', version), /Invalid dex-app/);
  }
  assert.ok(compareVersions('v2', 'v1') > 0);
  assert.equal(compareVersions('v1', 'v1'), 0);
});

test('snapshot keeps every release, leaves redirects unchanged, and leaves other instances alone', async () => {
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
    const redirectsContent = JSON.stringify([{from: '/legacy/', to: '/reference/web/1.28.x/guide/old-name'}]);
    await writeFile(join(siteDirectory, 'redirects.json'), redirectsContent);
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
    const allVersions = ['1.36.x', '1.35.x', '1.32.x', '1.30.x', '1.29.x', '1.28.x'];
    assert.deepEqual(result.versions, allVersions);
    assert.equal(await readFile(join(siteDirectory, 'redirects.json'), 'utf8'), redirectsContent);
    assert.equal(await readFile(join(siteDirectory, 'reference-web_versioned_docs/version-1.28.x/gone.mdx'), 'utf8'), '# Gone');
    assert.equal(await readFile(join(siteDirectory, 'reference-web_versioned_sidebars/version-1.28.x-sidebars.json'), 'utf8'), '{}');
    assert.deepEqual(JSON.parse(await readFile(join(siteDirectory, 'reference/versions.json'), 'utf8')), {web: '1.36.x', go: '1.35.x'});
    assert.equal(await readFile(join(siteDirectory, 'reference-go_versions.json'), 'utf8'), '["1.32.x"]\n');
    assert.deepEqual(await readReferenceDocuments(latestDirectory), [
      {id: 'guide/worker', slug: '/guide/new-name/'}, {id: 'index', slug: '/'},
    ]);
    await writeFile(join(latestDirectory, 'patched.mdx'), '# Patched');
    const patchResult = await snapshotReference('web', '1.36.1', {siteDirectory, createSnapshot});
    assert.equal(snapshotCount, 2, 'a patch release must refresh the hidden latest snapshot');
    assert.deepEqual(patchResult.versions, allVersions);
    assert.equal(await readFile(join(siteDirectory, 'reference-web_versioned_docs/version-1.36.x/patched.mdx'), 'utf8'), '# Patched');
    assert.deepEqual(JSON.parse(await readFile(join(siteDirectory, 'reference-web_versions.json'), 'utf8')), allVersions);
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
    assert.deepEqual((await snapshotReference('server', 'v0.1.19', {siteDirectory, createSnapshot})).versions, ['0.1.19']);
    assert.deepEqual((await snapshotReference('server', '0.1.20', {siteDirectory, createSnapshot})).versions, ['0.1.20', '0.1.19']);
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

test('the next release of every component keeps every stored version', async () => {
  const siteDirectory = await mkdtemp(join(tmpdir(), 'dex-reference-next-release-'));
  try {
    for (const entry of await readdir(docsDirectory)) {
      if (['reference', 'content', 'i18n', 'src', 'redirects.json'].includes(entry)
        || /^reference-[a-z-]+_(?:versioned_docs|versioned_sidebars|versions\.json)$/.test(entry)) {
        await cp(join(docsDirectory, entry), join(siteDirectory, entry), {recursive: true});
      }
    }
    const latestVersions = JSON.parse(await readFile(join(siteDirectory, 'reference/versions.json'), 'utf8'));
    const createSnapshot = async (directory, component, version) => {
      await cp(join(directory, 'reference', component),
        join(directory, `reference-${component}_versioned_docs/version-${version}`), {recursive: true});
      await cp(join(directory, `reference-${component}_versioned_sidebars/version-${latestVersions[component]}-sidebars.json`),
        join(directory, `reference-${component}_versioned_sidebars/version-${version}-sidebars.json`));
    };
    const redirectsContent = await readFile(join(siteDirectory, 'redirects.json'), 'utf8');
    for (const [component, latestVersion] of Object.entries(latestVersions)) {
      const [major, minor, patch] = latestVersion.split('.');
      const schemaVersion = latestVersion.match(/^v(\d+)$/)?.[1];
      const nextRelease = schemaVersion ? `v${Number(schemaVersion) + 1}`
        : patch === 'x' ? `${major}.${Number(minor) + 1}.0` : `${major}.${minor}.${Number(patch) + 1}`;
      const previousVersions = JSON.parse(await readFile(join(siteDirectory, `reference-${component}_versions.json`), 'utf8'));
      const {version, versions} = await snapshotReference(component, nextRelease, {siteDirectory, createSnapshot});
      assert.deepEqual(versions, [version, ...previousVersions], `${component} must keep every stored version`);
      for (const storedVersion of versions) {
        assert.ok((await Globby(['**/*.{md,mdx}'],
          {cwd: join(siteDirectory, `reference-${component}_versioned_docs/version-${storedVersion}`)})).length > 0,
        `${component} ${storedVersion} snapshot must remain`);
      }
    }
    assert.equal(await readFile(join(siteDirectory, 'redirects.json'), 'utf8'), redirectsContent);
  } finally {
    await rm(siteDirectory, {recursive: true, force: true});
  }
});
