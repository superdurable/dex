// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {execFile} from 'node:child_process';
import {cp, mkdir, mkdtemp, readFile, readdir, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {createRequire} from 'node:module';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {promisify} from 'node:util';

const executeFile = promisify(execFile);
const docsRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repositoryRoot = dirname(docsRoot);
const versions = JSON.parse(await readFile(join(docsRoot, 'api-reference-versions.json'), 'utf8'));
const toolVersions = {gomarkdoc: '1.1.0', pdoc: '15.0.4',
  ...JSON.parse(await readFile(join(docsRoot, 'scripts', 'api-reference-tools', 'package.json'), 'utf8')).dependencies};
const argumentsList = process.argv.slice(2);
const outputRoot = resolve(process.env.DEX_API_OUTPUT ?? join(docsRoot, '.cache', 'api-reference'));
const toolsRoot = join(docsRoot, '.cache', 'api-reference-tools');

if (argumentsList[0] === '--matrix') {
  const latestOnly = argumentsList[1] === 'latest';
  console.log(JSON.stringify({include: Object.entries(versions).flatMap(([language, releases]) =>
    (latestOnly ? releases.slice(0, 1) : releases).map((version) => ({language, version}))),
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
  throw new Error('Usage: node scripts/build-api-reference.mjs <go|python|typescript> <sdk-language/vX.Y.Z> | --all | --latest | --matrix [latest|all]');
}

async function buildReference(language, tag) {
  const version = tag.replace(`sdk-${language}/v`, '');
  if (!versions[language]?.includes(version) || tag !== `sdk-${language}/v${version}`) {
    throw new Error(`Unsupported language or tag: ${language} ${tag}; update api-reference-versions.json first`);
  }
  const minor = version.split('.').slice(0, 2).join('.');
  const destination = join(outputRoot, language, minor);
  const temporaryRoot = await mkdtemp(join(tmpdir(), 'dex-api-reference-'));
  try {
    const commit = (await run('git', ['rev-parse', `${tag}^{commit}`], repositoryRoot)).trim();
    const archivePath = join(temporaryRoot, 'sdk.tar');
    await run('git', ['archive', '--format=tar', `--output=${archivePath}`, tag, `sdk-${language}`], repositoryRoot);
    await run('tar', ['-xf', archivePath, '-C', temporaryRoot]);
    const sourceRoot = join(temporaryRoot, `sdk-${language}`);
    const generatedRoot = join(temporaryRoot, 'html');
    await mkdir(generatedRoot);
    console.log(`Building ${tag} (${commit})`);
    if (language === 'go') await buildGo(sourceRoot, generatedRoot, tag);
    if (language === 'python') await buildPython(sourceRoot, generatedRoot, version, temporaryRoot);
    if (language === 'typescript') await buildTypeScript(sourceRoot, generatedRoot, version);
    await decoratePages(generatedRoot, language, minor, version);
    await writeFile(join(generatedRoot, 'build-metadata.json'), JSON.stringify({language, version, tag, commit, toolVersions}, null, 2) + '\n');
    await rm(destination, {recursive: true, force: true});
    await mkdir(dirname(destination), {recursive: true});
    await cp(generatedRoot, destination, {recursive: true});
    console.log(`Wrote ${destination}`);
  } finally {
    await rm(temporaryRoot, {recursive: true, force: true});
  }
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
  const {default: GithubSlugger} = await nodeTool('github-slugger');
  const packages = (await run('go', ['list', '-f', '{{if .GoFiles}}{{.ImportPath}}{{end}}', './...'], sourceRoot, {GOWORK: 'off'})).trim().split('\n')
    .filter((packageName) => packageName && !/(?:^|\/)(?:internal|gen|examples|integ|scripts)(?:\/|$)/.test(packageName));
  const packageLinks = [];
  for (const packageName of packages) {
    const packagePath = packageName.replace('github.com/superdurable/dex/sdk-go/', '');
    const htmlPath = `${packagePath}/index.html`;
    const markdown = await run(gomarkdocPath, ['--format', 'github', '--repository.url', 'https://github.com/superdurable/dex',
      '--repository.default-branch', tag, '--repository.path', '/sdk-go', packageName], sourceRoot, {GOWORK: 'off'});
    await mkdir(join(generatedRoot, packagePath), {recursive: true});
    const slugger = new GithubSlugger();
    const renderer = new marked.Renderer();
    renderer.slugger = slugger;
    renderer.heading = renderGoHeading;
    await writeFile(join(generatedRoot, htmlPath), htmlDocument(`${packageName} API`, marked.parse(markdown, {renderer})));
    packageLinks.push(`<li><a href="${htmlPath}">${packageName}</a></li>`);
  }
  await writeFile(join(generatedRoot, 'index.html'), htmlDocument(`Dex Go SDK ${tag.split('/v')[1]} API`, `<h1>Go SDK API reference</h1><ul>${packageLinks.join('')}</ul>`));
}

async function buildPython(sourceRoot, generatedRoot, version, temporaryRoot) {
  const environmentRoot = join(temporaryRoot, 'venv');
  await run(process.env.DEX_API_PYTHON ?? 'python3', ['-m', 'venv', environmentRoot]);
  const pythonPath = join(environmentRoot, 'bin', 'python');
  await run(pythonPath, ['-m', 'pip', 'install', '--disable-pip-version-check', `pdoc==${toolVersions.pdoc}`, `dex-python-sdk==${version}`]);
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

async function nodeTool(packageName) {
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
    await mkdir(toolsRoot, {recursive: true});
    for (const filename of ['package.json', 'package-lock.json']) {
      await cp(join(docsRoot, 'scripts', 'api-reference-tools', filename), join(toolsRoot, filename));
    }
    await run('npm', ['ci', '--ignore-scripts', '--no-audit', '--no-fund'], toolsRoot);
    return await nodeTool(packageName);
  }
}

async function decoratePages(directory, language, minor, version, pageRoot = directory) {
  for (const entry of await readdir(directory, {withFileTypes: true})) {
    const entryPath = join(directory, entry.name);
    if (entry.isDirectory()) {
      await decoratePages(entryPath, language, minor, version, pageRoot);
    } else if (entry.name.endsWith('.html')) {
      const pathname = entryPath.slice(pageRoot.length + 1).split('\\').join('/');
      const canonical = `https://docs.superdurable.io/api/${language}/${minor}/${pathname.replace(/index\.html$/, '')}`;
      const robots = version === versions[language][0] ? 'index,follow' : 'noindex,follow';
      const metadata = `<meta name="dex-api-reference" content="${language}@${version}"><meta name="robots" content="${robots}"><link rel="canonical" href="${canonical}">`;
      let html = await readFile(entryPath, 'utf8');
      html = html.replace(/<link\b[^>]*rel=["']canonical["'][^>]*>/gi, '').replace(/<meta\b[^>]*name=["']robots["'][^>]*>/gi, '');
      html = html.replace(/<\/head>/i, `${metadata}</head>`);
      const notice = `<aside style="padding:1rem;background:#f2f4f8;color:#17212e;font:16px system-ui">Dex ${language} SDK ${version} API · <a href="/reference/${language}/">Developer reference</a>${version === versions[language][0] ? '' : ` · Older version. <a href="/api/${language}/${versions[language][0].split('.').slice(0, 2).join('.')}/">Latest API reference</a>`}</aside>`;
      html = html.replace(/(<body\b[^>]*>)/i, `$1${notice}`);
      await writeFile(entryPath, html);
    }
  }
}

function htmlDocument(title, body) {
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>${title}</title><style>body{margin:0;color:#17212e;background:#fff;font:16px/1.6 system-ui}main{max-width:72rem;padding:2rem;margin:auto;overflow-wrap:anywhere}pre{overflow:auto;padding:1rem;background:#f2f4f8}a{color:#175cb6}code{font-size:.9em}table{border-collapse:collapse}td,th{border:1px solid #ccd4df;padding:.5rem}</style></head><body><main>${body}</main></body></html>`;
}

function renderGoHeading({tokens, depth, text}) {
  return `<h${depth} id="${this.slugger.slug(text)}">${this.parser.parseInline(tokens)}</h${depth}>\n`;
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
