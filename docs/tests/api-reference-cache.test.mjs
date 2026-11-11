// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import assert from 'node:assert/strict';
import {mkdir, mkdtemp, readdir, readFile, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import test from 'node:test';
import {entriesHash, refreshApiReferenceCache} from '../scripts/refresh-api-reference-cache.mjs';

const goLatest = {language: 'go', version: '1.37.0', minor: '1.37', revision: 'a'.repeat(40)};
const javaLatest = {language: 'java', version: '1.37.0', minor: '1.37', revision: 'maven-central'};

async function withCacheRoot(callback) {
  const cacheRoot = await mkdtemp(join(tmpdir(), 'dex-api-cache-test-'));
  try {
    await callback(cacheRoot);
  } finally {
    await rm(cacheRoot, {recursive: true, force: true});
  }
}

function recordingGenerator(cacheRoot) {
  const generated = [];
  const generate = async ({language, version, minor}) => {
    generated.push(`${language}@${version}`);
    const treeRoot = join(cacheRoot, language, minor);
    await rm(treeRoot, {recursive: true, force: true});
    await mkdir(treeRoot, {recursive: true});
    await writeFile(join(treeRoot, 'build-metadata.json'), JSON.stringify({language, version}));
  };
  return {generated, generate};
}

test('regenerates on a miss and reuses on a matching stamp', async () => withCacheRoot(async (cacheRoot) => {
  const first = recordingGenerator(cacheRoot);
  await refreshApiReferenceCache({cacheRoot, toolHash: 't1', entries: [goLatest, javaLatest], generate: first.generate});
  assert.deepEqual(first.generated, ['go@1.37.0', 'java@1.37.0']);

  const second = recordingGenerator(cacheRoot);
  const outcome = await refreshApiReferenceCache({cacheRoot, toolHash: 't1', entries: [goLatest, javaLatest], generate: second.generate});
  assert.deepEqual(second.generated, []);
  assert.deepEqual(outcome.reused, ['go/1.37', 'java/1.37']);
}));

test('regenerates a tree when its revision, patch version, or tools change', async () => withCacheRoot(async (cacheRoot) => {
  await refreshApiReferenceCache({cacheRoot, toolHash: 't1', entries: [goLatest], generate: recordingGenerator(cacheRoot).generate});
  const changes = [
    [{...goLatest, revision: 'b'.repeat(40)}, 't1'],
    [{...goLatest, version: '1.37.1', revision: 'c'.repeat(40)}, 't1'],
    [{...goLatest, version: '1.37.1', revision: 'c'.repeat(40)}, 't2'],
  ];
  for (const [entry, toolHash] of changes) {
    const recorder = recordingGenerator(cacheRoot);
    await refreshApiReferenceCache({cacheRoot, toolHash, entries: [entry], generate: recorder.generate});
    assert.deepEqual(recorder.generated, [`go@${entry.version}`]);
  }
  const metadata = JSON.parse(await readFile(join(cacheRoot, 'go', '1.37', 'build-metadata.json'), 'utf8'));
  assert.equal(metadata.version, '1.37.1');
}));

test('regenerates a stamped tree whose files are missing', async () => withCacheRoot(async (cacheRoot) => {
  await refreshApiReferenceCache({cacheRoot, toolHash: 't1', entries: [goLatest], generate: recordingGenerator(cacheRoot).generate});
  await rm(join(cacheRoot, 'go', '1.37'), {recursive: true});
  const recorder = recordingGenerator(cacheRoot);
  await refreshApiReferenceCache({cacheRoot, toolHash: 't1', entries: [goLatest], generate: recorder.generate});
  assert.deepEqual(recorder.generated, ['go@1.37.0']);
}));

test('does not stamp a tree whose generation failed', async () => withCacheRoot(async (cacheRoot) => {
  const failing = async () => {
    throw new Error('generator failed');
  };
  await assert.rejects(refreshApiReferenceCache({cacheRoot, toolHash: 't1', entries: [goLatest], generate: failing}), /generator failed/);
  const recorder = recordingGenerator(cacheRoot);
  await refreshApiReferenceCache({cacheRoot, toolHash: 't1', entries: [goLatest], generate: recorder.generate});
  assert.deepEqual(recorder.generated, ['go@1.37.0']);
}));

test('removes trees and stamps outside the entry list', async () => withCacheRoot(async (cacheRoot) => {
  const older = {...goLatest, version: '1.35.0', minor: '1.35'};
  await refreshApiReferenceCache({cacheRoot, toolHash: 't1', entries: [goLatest, older, javaLatest], generate: recordingGenerator(cacheRoot).generate});
  const outcome = await refreshApiReferenceCache({cacheRoot, toolHash: 't1', entries: [goLatest], generate: recordingGenerator(cacheRoot).generate});
  assert.deepEqual(outcome.removed, ['go/1.35', 'java/1.37']);
  assert.deepEqual((await readdir(join(cacheRoot, 'go'))).sort(), ['1.37', '1.37.stamp.json']);
  assert.deepEqual(await readdir(join(cacheRoot, 'java')), []);
}));

test('pruning leaves unrelated files and directories alone', async () => withCacheRoot(async (cacheRoot) => {
  const older = {...goLatest, version: '1.35.0', minor: '1.35'};
  await refreshApiReferenceCache({cacheRoot, toolHash: 't1', entries: [goLatest, older], generate: recordingGenerator(cacheRoot).generate});
  const unrelatedPaths = [join('go', 'notes.txt'), join('go', 'drafts', 'page.html'), join('other', '1.35', 'keep.html'), 'README.md'];
  for (const unrelatedPath of unrelatedPaths) {
    await mkdir(join(cacheRoot, unrelatedPath, '..'), {recursive: true});
    await writeFile(join(cacheRoot, unrelatedPath), 'keep');
  }
  const outcome = await refreshApiReferenceCache({cacheRoot, toolHash: 't1', entries: [goLatest], generate: recordingGenerator(cacheRoot).generate});
  assert.deepEqual(outcome.removed, ['go/1.35']);
  for (const unrelatedPath of unrelatedPaths) {
    assert.equal(await readFile(join(cacheRoot, unrelatedPath), 'utf8'), 'keep');
  }
}));

test('the entries hash changes with any revision or the tools', () => {
  const base = entriesHash([goLatest, javaLatest], 't1');
  assert.equal(entriesHash([goLatest, javaLatest], 't1'), base);
  assert.notEqual(entriesHash([{...goLatest, revision: 'b'.repeat(40)}, javaLatest], 't1'), base);
  assert.notEqual(entriesHash([goLatest, javaLatest], 't2'), base);
  assert.notEqual(entriesHash([goLatest], 't1'), base);
});
