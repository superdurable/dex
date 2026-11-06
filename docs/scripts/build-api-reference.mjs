// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

// Generates undecorated API HTML for one SDK release. assemble-api-reference.mjs applies manifest-dependent markup.

import {execFile} from 'node:child_process';
import {createHash} from 'node:crypto';
import {cp, lstat, mkdir, mkdtemp, readFile, readdir, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {createRequire} from 'node:module';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {promisify} from 'node:util';

const executeFile = promisify(execFile);
const docsRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repositoryRoot = dirname(docsRoot);
const toolsSourceRoot = join(docsRoot, 'scripts', 'api-reference-tools');
const versions = JSON.parse(await readFile(join(docsRoot, 'api-reference-versions.json'), 'utf8'));
const toolVersions = {
  ...JSON.parse(await readFile(join(toolsSourceRoot, 'go-tools.json'), 'utf8')),
  ...JSON.parse(await readFile(join(toolsSourceRoot, 'package.json'), 'utf8')).dependencies,
  pdoc: (await readFile(join(toolsSourceRoot, 'pdoc-requirements.txt'), 'utf8')).match(/^pdoc==(\S+)/m)[1],
};
const argumentsList = process.argv.slice(2);
const outputRoot = resolve(process.env.DEX_API_OUTPUT ?? join(docsRoot, '.cache', 'api-reference'));
const toolsRoot = join(docsRoot, '.cache', 'api-reference-tools');
const maximumToolInstallAttempts = 3;
const javadocRepositoryUrl = 'https://repo1.maven.org/maven2/io/superdurable/dex-sdk';

// gomarkdoc links symbols to its own named anchors; only section links such as #constants need heading ids.
class HeadingIdAllocator {
  constructor() {
    this.countsById = new Map();
  }

  allocate(headingHtml) {
    const baseId = headingHtml.replace(/<[^>]*>/g, '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
    const count = this.countsById.get(baseId) ?? 0;
    this.countsById.set(baseId, count + 1);
    return count === 0 ? baseId : `${baseId}-${count}`;
  }
}

if (argumentsList[0] === '--matrix') {
  const isLatestOnly = argumentsList[1] === 'latest';
  console.log(JSON.stringify({include: Object.entries(versions).flatMap(([language, releases]) =>
    (isLatestOnly ? releases.slice(0, 1) : releases).map((version) => ({language, version, minor: minorOf(version)}))),
  }));
} else if (['--all', '--latest'].includes(argumentsList[0])) {
  for (const [language, releases] of Object.entries(versions)) {
    for (const version of argumentsList[0] === '--latest' ? releases.slice(0, 1) : releases) {
      await buildReference(language, `sdk-${language}/v${version}`);
    }
  }
} else if (argumentsList.length === 2) {
  await buildReference(...argumentsList);
} else {
  throw new Error('Usage: node scripts/build-api-reference.mjs <go|python|typescript|java> <sdk-language/vX.Y.Z> | --all | --latest | --matrix [latest|all]');
}

async function buildReference(language, tag) {
  const version = tag.replace(`sdk-${language}/v`, '');
  if (!versions[language]?.includes(version) || tag !== `sdk-${language}/v${version}`) {
    throw new Error(`Unsupported language or tag: ${language} ${tag}; update api-reference-versions.json first`);
  }
  const destination = join(outputRoot, language, minorOf(version));
  const temporaryRoot = await mkdtemp(join(tmpdir(), 'dex-api-reference-'));
  try {
    const generatedRoot = join(temporaryRoot, 'html');
    await mkdir(generatedRoot);
    const source = language === 'java'
      ? await buildJava(generatedRoot, version, temporaryRoot)
      : await buildFromTag(language, tag, version, generatedRoot, temporaryRoot);
    await writeFile(join(generatedRoot, 'build-metadata.json'),
      JSON.stringify({language, version, tag, ...source, toolVersions}, null, 2) + '\n');
    await rm(destination, {recursive: true, force: true});
    await mkdir(dirname(destination), {recursive: true});
    await cp(generatedRoot, destination, {recursive: true});
    console.log(`Wrote ${destination}`);
  } finally {
    await rm(temporaryRoot, {recursive: true, force: true});
  }
}

async function buildFromTag(language, tag, version, generatedRoot, temporaryRoot) {
  const commit = (await run('git', ['rev-parse', `${tag}^{commit}`], repositoryRoot)).trim();
  const archivePath = join(temporaryRoot, 'sdk.tar');
  await run('git', ['archive', '--format=tar', `--output=${archivePath}`, tag, `sdk-${language}`], repositoryRoot);
  await run('tar', ['-xf', archivePath, '-C', temporaryRoot]);
  const sourceRoot = join(temporaryRoot, `sdk-${language}`);
  console.log(`Building ${tag} (${commit})`);
  if (language === 'go') await buildGo(sourceRoot, generatedRoot, tag);
  if (language === 'python') await buildPython(sourceRoot, generatedRoot, version, temporaryRoot);
  if (language === 'typescript') await buildTypeScript(sourceRoot, generatedRoot, version);
  return {commit};
}

async function buildGo(sourceRoot, generatedRoot, tag) {
  const binaryRoot = join(toolsRoot, `gomarkdoc-${toolVersions.gomarkdoc}`);
  await mkdir(binaryRoot, {recursive: true});
  const gomarkdocPath = join(binaryRoot, 'gomarkdoc');
  try {
    await readFile(gomarkdocPath);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
    await run('go', ['install', `github.com/princjef/gomarkdoc/cmd/gomarkdoc@v${toolVersions.gomarkdoc}`], repositoryRoot, {GOBIN: binaryRoot, GOWORK: 'off'});
  }
  const {marked} = await nodeTool('marked');
  const packages = (await run('go', ['list', '-f', '{{if .GoFiles}}{{.ImportPath}}{{end}}', './...'], sourceRoot, {GOWORK: 'off'})).trim().split('\n')
    .filter((packageName) => packageName && !/(?:^|\/)(?:internal|gen|examples|integ|scripts)(?:\/|$)/.test(packageName));
  const packageLinks = [];
  for (const packageName of packages) {
    const packagePath = packageName.replace('github.com/superdurable/dex/sdk-go/', '');
    const htmlPath = `${packagePath}/index.html`;
    const markdown = await run(gomarkdocPath, ['--format', 'github', '--repository.url', 'https://github.com/superdurable/dex',
      '--repository.default-branch', tag, '--repository.path', '/sdk-go', packageName], sourceRoot, {GOWORK: 'off'});
    await mkdir(join(generatedRoot, packagePath), {recursive: true});
    const headingIds = new HeadingIdAllocator();
    const renderer = new marked.Renderer();
    renderer.heading = function renderGoHeading({tokens, depth}) {
      const content = this.parser.parseInline(tokens);
      return `<h${depth} id="${headingIds.allocate(content)}">${content}</h${depth}>\n`;
    };
    await writeFile(join(generatedRoot, htmlPath), htmlDocument(`${packageName} API`, marked.parse(markdown, {renderer})));
    packageLinks.push(`<li><a href="${htmlPath}">${packageName}</a></li>`);
  }
  await writeFile(join(generatedRoot, 'index.html'), htmlDocument(`Dex Go SDK ${tag.split('/v')[1]} API`, `<h1>Go SDK API reference</h1><ul>${packageLinks.join('')}</ul>`));
}

async function buildPython(sourceRoot, generatedRoot, version, temporaryRoot) {
  const environmentRoot = join(temporaryRoot, 'venv');
  await run(process.env.DEX_API_PYTHON ?? 'python3', ['-m', 'venv', environmentRoot]);
  const pythonPath = join(environmentRoot, 'bin', 'python');
  await run(pythonPath, ['-m', 'pip', 'install', '--disable-pip-version-check', '--require-hashes', '--only-binary=:all:',
    '-r', join(toolsSourceRoot, 'pdoc-requirements.txt')]);
  // The SDK release pins its own dependencies; the wheel provides the native extension.
  await run(pythonPath, ['-m', 'pip', 'install', '--disable-pip-version-check', '--only-binary=:all:', `dex-python-sdk==${version}`]);
  const sitePackages = (await run(pythonPath, ['-c', 'import sysconfig; print(sysconfig.get_path("purelib"))'])).trim();
  const installedPackage = join(sitePackages, 'dex');
  for (const filename of await readdir(installedPackage)) {
    if (filename.startsWith('_native.') && filename.endsWith('.so')) {
      await cp(join(installedPackage, filename), join(sourceRoot, 'dex', filename));
    }
  }
  await run(pythonPath, ['-m', 'pdoc', '--docformat', 'google', '--no-show-source', '-o', generatedRoot,
    'dex', '!dex.dexpb', '!dex._native'], sourceRoot, {PYTHONPATH: sourceRoot});
  await writeFile(join(generatedRoot, 'index.html'), htmlDocument(`Dex Python SDK ${version} API`, '<h1>Python SDK API reference</h1><p><a href="dex.html">Open the dex package</a></p>'));
}

async function buildTypeScript(sourceRoot, generatedRoot, version) {
  await run('npm', ['ci', '--ignore-scripts', '--no-audit', '--no-fund'], sourceRoot);
  await nodeTool('typedoc');
  const tsconfigPath = join(sourceRoot, 'tsconfig.api.json');
  await writeFile(tsconfigPath, JSON.stringify({extends: './tsconfig.json', include: ['src/**/*.ts'], exclude: ['test']}));
  await run(process.execPath, [join(toolsRoot, 'node_modules', 'typedoc', 'bin', 'typedoc'),
    '--entryPoints', join(sourceRoot, 'src', 'index.ts'), '--tsconfig', tsconfigPath,
    '--out', generatedRoot, '--name', `Dex TypeScript SDK ${version}`, '--readme', 'none', '--disableSources'], sourceRoot);
}

async function buildJava(generatedRoot, version, temporaryRoot) {
  const jarUrl = `${javadocRepositoryUrl}/${version}/dex-sdk-${version}-javadoc.jar`;
  console.log(`Downloading ${jarUrl}`);
  const jarBytes = await download(jarUrl);
  const expectedSha512 = (await download(`${jarUrl}.sha512`)).toString('utf8').trim().split(/\s+/)[0].toLowerCase();
  const actualSha512 = createHash('sha512').update(jarBytes).digest('hex');
  if (actualSha512 !== expectedSha512) {
    throw new Error(`Checksum mismatch for ${jarUrl}: expected ${expectedSha512}, got ${actualSha512}`);
  }
  const jarPath = join(temporaryRoot, 'javadoc.jar');
  await writeFile(jarPath, jarBytes);
  await run('unzip', ['-q', jarPath, '-d', generatedRoot]);
  await rm(join(generatedRoot, 'META-INF'), {recursive: true, force: true});
  await rejectSymbolicLinks(generatedRoot);
  return {source: jarUrl, sha512: actualSha512};
}

async function download(url) {
  const response = await fetch(url);
  if (!response.ok) throw new Error(`Download failed with HTTP ${response.status}: ${url}`);
  return Buffer.from(await response.arrayBuffer());
}

async function rejectSymbolicLinks(directory) {
  for (const entry of await readdir(directory, {withFileTypes: true})) {
    const entryPath = join(directory, entry.name);
    if ((await lstat(entryPath)).isSymbolicLink()) throw new Error(`Archive contains a symbolic link: ${entryPath}`);
    if (entry.isDirectory()) await rejectSymbolicLinks(entryPath);
  }
}

async function nodeTool(packageName, attempt = 1) {
  try {
    const installedPackage = JSON.parse(await readFile(join(toolsRoot, 'node_modules', packageName, 'package.json'), 'utf8'));
    if (installedPackage.version !== toolVersions[packageName]) {
      const error = new Error(`API tool ${packageName} must be ${toolVersions[packageName]}`);
      error.code = 'DEX_API_TOOL_VERSION_MISMATCH';
      throw error;
    }
    const require = createRequire(join(toolsRoot, 'package.json'));
    const modulePath = packageName === 'marked' ? join(toolsRoot, 'node_modules', 'marked', 'lib', 'marked.esm.js') : require.resolve(packageName);
    return await import(pathToFileURL(modulePath).href);
  } catch (error) {
    if (!['ENOENT', 'ERR_MODULE_NOT_FOUND', 'MODULE_NOT_FOUND', 'DEX_API_TOOL_VERSION_MISMATCH'].includes(error.code)) throw error;
    if (attempt >= maximumToolInstallAttempts) throw error;
    await mkdir(toolsRoot, {recursive: true});
    for (const filename of ['package.json', 'package-lock.json']) {
      await cp(join(toolsSourceRoot, filename), join(toolsRoot, filename));
    }
    await run('npm', ['ci', '--ignore-scripts', '--no-audit', '--no-fund'], toolsRoot);
    return await nodeTool(packageName, attempt + 1);
  }
}

function htmlDocument(title, body) {
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>${title}</title><style>body{margin:0;color:#17212e;background:#fff;font:16px/1.6 system-ui}main{max-width:72rem;padding:2rem;margin:auto;overflow-wrap:anywhere}pre{overflow:auto;padding:1rem;background:#f2f4f8}a{color:#175cb6}code{font-size:.9em}table{border-collapse:collapse}td,th{border:1px solid #ccd4df;padding:.5rem}</style></head><body><main>${body}</main></body></html>`;
}

function minorOf(version) {
  return version.split('.').slice(0, 2).join('.');
}

async function run(command, commandArguments, cwd = repositoryRoot, environment = {}) {
  try {
    const {stdout, stderr} = await executeFile(command, commandArguments, {
      cwd, env: {...process.env, ...environment}, maxBuffer: 64 * 1024 * 1024,
    });
    if (stderr) process.stderr.write(stderr);
    return stdout;
  } catch (error) {
    if (error.stdout) process.stderr.write(error.stdout);
    if (error.stderr) process.stderr.write(error.stderr);
    throw error;
  }
}
