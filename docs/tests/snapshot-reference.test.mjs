import assert from 'node:assert/strict';
import {cp, mkdir, mkdtemp, readFile, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import test from 'node:test';
import {
  droppedVersionRedirects, readReferenceDocuments, referenceVersion,
  retargetRedirects, snapshotReference,
} from '../scripts/snapshot-reference.mjs';

test('release versions follow component boundaries', () => {
  assert.equal(referenceVersion('go', 'v1.35.2'), '1.35.x');
  assert.equal(referenceVersion('cli', '1.36.x'), '1.36.x');
  assert.equal(referenceVersion('server', 'v0.1.19'), '0.1.19');
  assert.equal(referenceVersion('server', '1.0.2'), '1.0.x');
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
    await snapshotReference('web', '1.36.1', {siteDirectory, createSnapshot});
    assert.equal(snapshotCount, 1, 'patch releases and retries must not make another snapshot');
    await assert.rejects(snapshotReference('web', '1.32.0', {siteDirectory, createSnapshot}), /older/);
    await assert.rejects(snapshotReference('../invalid', '1.36.0', {siteDirectory, createSnapshot}), /Unknown/);
  } finally {
    await rm(siteDirectory, {recursive: true, force: true});
  }
});
