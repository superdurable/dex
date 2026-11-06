import {spawnSync} from 'node:child_process';
import {readFile, readdir, rm, writeFile} from 'node:fs/promises';
import {basename, dirname, extname, join, relative} from 'node:path';
import {fileURLToPath} from 'node:url';
import matter from 'gray-matter';
import slugModule from '@docusaurus/plugin-content-docs/lib/slug.js';
import {DefaultNumberPrefixParser, stripPathNumberPrefixes} from '@docusaurus/plugin-content-docs/lib/numberPrefix.js';

const getSlug = slugModule.default;
const docsDirectory = fileURLToPath(new URL('../', import.meta.url));
const retainedVersionCount = 5;

export function referenceVersion(component, releaseVersion) {
  const release = releaseVersion.replace(/^v/, '');
  if (component === 'server' && /^0\.1\.\d+$/.test(release)) {
    return release;
  }
  const match = release.match(/^([1-9]\d*)\.(\d+)\.(?:\d+|x)$/);
  if (!match) {
    throw new Error(`Invalid ${component} reference version: ${releaseVersion}`);
  }
  return `${match[1]}.${match[2]}.x`;
}

export function compareVersions(first, second) {
  const firstParts = first.split('.').map((part) => part === 'x' ? 0 : Number(part));
  const secondParts = second.split('.').map((part) => part === 'x' ? 0 : Number(part));
  for (let index = 0; index < 3; index += 1) {
    if (firstParts[index] !== secondParts[index]) {
      return firstParts[index] - secondParts[index];
    }
  }
  return 0;
}

export async function snapshotReference(component, releaseVersion, {
  siteDirectory = docsDirectory,
  createSnapshot = runDocusaurusVersion,
} = {}) {
  const sharedVersionsPath = join(siteDirectory, 'reference/versions.json');
  const latestVersions = JSON.parse(await readFile(sharedVersionsPath, 'utf8'));
  if (!Object.hasOwn(latestVersions, component)) {
    throw new Error(`Unknown reference component: ${component}`);
  }
  const version = referenceVersion(component, releaseVersion);
  if (compareVersions(version, latestVersions[component]) < 0) {
    throw new Error(`Cannot replace latest ${latestVersions[component]} with older ${version}`);
  }
  const versionsPath = join(siteDirectory, `reference-${component}_versions.json`);
  const previousVersions = await readOptionalVersions(versionsPath);
  if (!previousVersions.includes(version)) {
    await createSnapshot(siteDirectory, component, version);
  }
  const allVersions = [...new Set([version, ...previousVersions])]
    .sort((first, second) => compareVersions(second, first));
  const retainedVersions = allVersions.slice(0, retainedVersionCount);
  const droppedVersions = allVersions.slice(retainedVersionCount);
  const latestDocuments = await readReferenceDocuments(join(siteDirectory, 'reference', component));
  const replacements = new Map();
  for (const droppedVersion of droppedVersions) {
    const droppedDocuments = await readReferenceDocuments(snapshotDirectory(siteDirectory, component, droppedVersion));
    for (const redirect of droppedVersionRedirects(component, droppedVersion, droppedDocuments, latestDocuments)) {
      replacements.set(redirect.from, redirect.to);
    }
  }
  const redirectsPath = join(siteDirectory, 'redirects.json');
  const redirects = JSON.parse(await readFile(redirectsPath, 'utf8'));
  const nextRedirects = retargetRedirects(redirects, replacements);
  await writeJSON(redirectsPath, nextRedirects);
  await writeJSON(versionsPath, retainedVersions);
  await writeJSON(sharedVersionsPath, {...latestVersions, [component]: version});
  for (const droppedVersion of droppedVersions) {
    await rm(snapshotDirectory(siteDirectory, component, droppedVersion), {recursive: true});
    await rm(join(siteDirectory, `reference-${component}_versioned_sidebars/version-${droppedVersion}-sidebars.json`));
  }
  return {component, version, retainedVersions, droppedVersions};
}

export function droppedVersionRedirects(component, version, droppedDocuments, latestDocuments) {
  const latestById = new Map(latestDocuments.map((document) => [document.id, document]));
  const redirects = [];
  for (const localePrefix of ['', '/zh-Hans']) {
    const componentRoute = `${localePrefix}/reference/${component}`;
    for (const document of droppedDocuments) {
      const latestDocument = latestById.get(document.id);
      redirects.push({
        from: `${componentRoute}/${version}${document.slug}`,
        to: `${componentRoute}${latestDocument?.slug ?? '/'}`,
      });
    }
  }
  return redirects;
}

export function retargetRedirects(redirects, replacements) {
  const redirectsBySource = new Map([...redirects.map(({from, to}) => [from, to]), ...replacements]);
  const destinations = new Map([...redirectsBySource].map(([from, to]) => [redirectKey(from), to]));
  return [...redirectsBySource].map(([from, destination]) => {
    const visited = new Set([redirectKey(from)]);
    while (destinations.has(redirectKey(destination))) {
      if (visited.has(redirectKey(destination))) {
        throw new Error(`Redirect cycle at ${destination}`);
      }
      visited.add(redirectKey(destination));
      destination = destinations.get(redirectKey(destination));
    }
    return {from, to: destination};
  });
}

function redirectKey(path) {
  return `${path.replace(/\/+$/, '')}/`;
}

export async function readReferenceDocuments(directory) {
  const documents = [];
  for (const source of await markdownFiles(directory)) {
    const {data: frontMatter} = matter(await readFile(join(directory, source), 'utf8'));
    const sourceDirName = dirname(source);
    const filename = basename(source, extname(source));
    const shouldParseNumberPrefixes = frontMatter.parse_number_prefixes !== false;
    const baseID = frontMatter.id ?? (shouldParseNumberPrefixes
      ? DefaultNumberPrefixParser(filename).filename : filename);
    const directoryId = shouldParseNumberPrefixes
      ? stripPathNumberPrefixes(sourceDirName, DefaultNumberPrefixParser) : sourceDirName;
    const slug = getSlug({baseID, source, sourceDirName,
      frontMatterSlug: frontMatter.slug, stripDirNumberPrefixes: shouldParseNumberPrefixes});
    documents.push({id: [directoryId === '.' ? '' : directoryId, baseID].filter(Boolean).join('/'),
      slug: `${slug.replace(/\/+$/, '')}/`});
  }
  return documents;
}

async function markdownFiles(directory, rootDirectory = directory) {
  const documents = [];
  for (const entry of await readdir(directory, {withFileTypes: true})) {
    const entryPath = join(directory, entry.name);
    if (entry.isDirectory()) {
      documents.push(...await markdownFiles(entryPath, rootDirectory));
    } else if (/\.mdx?$/.test(entry.name)) {
      documents.push(relative(rootDirectory, entryPath).split('\\').join('/'));
    }
  }
  return documents;
}

async function readOptionalVersions(path) {
  try {
    return JSON.parse(await readFile(path, 'utf8'));
  } catch (error) {
    if (error.code === 'ENOENT') {
      return [];
    }
    throw error;
  }
}

function snapshotDirectory(siteDirectory, component, version) {
  return join(siteDirectory, `reference-${component}_versioned_docs/version-${version}`);
}

function runDocusaurusVersion(siteDirectory, component, version) {
  const result = spawnSync(process.execPath, [join(docsDirectory, 'node_modules/@docusaurus/core/bin/docusaurus.mjs'),
    `docs:version:reference-${component}`, version], {cwd: siteDirectory, stdio: 'inherit'});
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0) {
    throw new Error(`Docusaurus version snapshot failed (${result.status})`);
  }
}

async function writeJSON(path, value) {
  await writeFile(path, `${JSON.stringify(value, null, 2)}\n`);
}

if (process.argv[1] && fileURLToPath(import.meta.url) === fileURLToPath(new URL(process.argv[1], 'file:///'))) {
  const [component, version, ...extraArguments] = process.argv.slice(2);
  if (!component || !version || extraArguments.length) {
    throw new Error('Usage: node scripts/snapshot-reference.mjs <component> <version>');
  }
  console.log(JSON.stringify(await snapshotReference(component, version)));
}
