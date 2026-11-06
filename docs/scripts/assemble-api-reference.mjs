// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

// Copies generated API HTML into the site and applies the manifest-dependent robots, canonical, and banner markup.

import {cp, mkdir, readFile, readdir, rm, stat, writeFile} from 'node:fs/promises';
import {dirname, join, relative, resolve, sep} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';

const docsRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
export const siteOrigin = 'https://docs.superdurable.io';
export const latestSegment = 'latest';
export const languageDisplayNames = {go: 'Go', python: 'Python', typescript: 'TypeScript', java: 'Java'};
export const versionMarkerName = 'dex-api-reference';
export const bannerClassName = 'dex-api-reference-banner';

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  const argumentsList = process.argv.slice(2);
  const optionValue = (name, fallback) => {
    const index = argumentsList.indexOf(name);
    return index >= 0 ? argumentsList[index + 1] : fallback;
  };
  await assembleApiReference({
    sourceRoot: resolve(optionValue('--source', join(docsRoot, '.cache', 'api-reference'))),
    destinationRoot: resolve(optionValue('--destination', join(docsRoot, 'build', 'api'))),
    canSkipMissingVersions: argumentsList.includes('--allow-missing-versions'),
  });
}

/**
 * Builds /api/<language>/<minor>/ for every manifest version and /api/<language>/latest/ for the newest one.
 *
 * @returns {Promise<string[]>} The assembled "<language>/<segment>" directories.
 */
export async function assembleApiReference({sourceRoot, destinationRoot, canSkipMissingVersions}) {
  const versions = JSON.parse(await readFile(join(docsRoot, 'api-reference-versions.json'), 'utf8'));
  await rm(destinationRoot, {recursive: true, force: true});
  const assembled = [];
  for (const [language, releases] of Object.entries(versions)) {
    for (const version of releases) {
      const versionSourceRoot = join(sourceRoot, language, minorOf(version));
      if (!(await isDirectory(versionSourceRoot))) {
        if (canSkipMissingVersions) {
          console.log(`Skipping missing ${language} ${version}`);
          continue;
        }
        throw new Error(`Missing generated API reference for ${language} ${version} in ${versionSourceRoot}`);
      }
      const metadata = JSON.parse(await readFile(join(versionSourceRoot, 'build-metadata.json'), 'utf8'));
      if (metadata.language !== language || metadata.version !== version) {
        throw new Error(`${versionSourceRoot} holds ${metadata.language} ${metadata.version}, expected ${language} ${version}`);
      }
      const segments = version === releases[0] ? [minorOf(version), latestSegment] : [minorOf(version)];
      for (const segment of segments) {
        const destination = join(destinationRoot, language, segment);
        await mkdir(dirname(destination), {recursive: true});
        await cp(versionSourceRoot, destination, {recursive: true});
        assembled.push(`${language}/${segment}`);
      }
    }
  }
  const latestPagesByLanguage = new Map();
  for (const language of Object.keys(versions)) {
    const latestRoot = join(destinationRoot, language, latestSegment);
    latestPagesByLanguage.set(language, await isDirectory(latestRoot) ? new Set(await htmlPagePaths(latestRoot)) : new Set());
  }
  for (const directory of assembled) {
    const [language, segment] = directory.split('/');
    const version = segment === latestSegment ? versions[language][0] : versions[language].find((release) => minorOf(release) === segment);
    const versionRoot = join(destinationRoot, language, segment);
    for (const pagePath of await htmlPagePaths(versionRoot)) {
      const html = await readFile(join(versionRoot, pagePath), 'utf8');
      await writeFile(join(versionRoot, pagePath), decoratePage(html, {
        language, segment, version, pagePath, latestPages: latestPagesByLanguage.get(language),
      }));
    }
  }
  console.log(`Assembled ${assembled.length} API reference trees in ${destinationRoot}`);
  return assembled;
}

function decoratePage(html, {language, segment, version, pagePath, latestPages}) {
  const pageLabel = `${language}/${segment}/${pagePath}`;
  if (html.includes(`name="${versionMarkerName}"`)) {
    throw new Error(`${pageLabel} is already decorated; regenerate it with the current generator`);
  }
  const isLatestTree = segment === latestSegment;
  const directoryPath = pagePath.replace(/(?:^|\/)index\.html$/, (match) => match.startsWith('/') ? '/' : '');
  const canonical = `${siteOrigin}/api/${language}/${segment}/${directoryPath}`;
  const robots = isLatestTree ? 'index,follow' : 'noindex,follow';
  const metadata = `<meta name="${versionMarkerName}" content="${language}@${version}"><meta name="robots" content="${robots}"><link rel="canonical" href="${canonical}">`;
  const withoutIndexingTags = html.replace(/<link\b[^>]*rel=["']canonical["'][^>]*>/gi, '').replace(/<meta\b[^>]*name=["']robots["'][^>]*>/gi, '');
  if (!/<\/head>/i.test(withoutIndexingTags) || !/<body\b[^>]*>/i.test(withoutIndexingTags)) {
    throw new Error(`${pageLabel} has no head or body element`);
  }
  const languageName = languageDisplayNames[language] ?? language;
  const linkStyle = 'color:#175cb6;text-decoration:underline';
  const referenceLink = `<a style="${linkStyle}" href="/reference/${language}/">${languageName} SDK Developer reference</a>`;
  const notice = isLatestTree
    ? `Dex ${languageName} SDK ${version} API reference (latest release) · ${referenceLink}`
    : `Dex ${languageName} SDK ${version} API reference · This is a versioned snapshot. · <a style="${linkStyle}" href="/api/${language}/${latestSegment}/${latestPages.has(pagePath) ? directoryPath : ''}">Read the latest version</a> · ${referenceLink}`;
  const banner = `<aside class="${bannerClassName}" style="position:relative;z-index:1000;padding:.75rem 1rem;background:#f2f4f8;color:#17212e;font:15px/1.5 system-ui,sans-serif;border-bottom:1px solid #ccd4df">${notice}</aside>`;
  return withoutIndexingTags.replace(/<\/head>/i, `${metadata}</head>`).replace(/(<body\b[^>]*>)/i, `$1${banner}`);
}

async function htmlPagePaths(root) {
  const pagePaths = [];
  for (const entry of await readdir(root, {withFileTypes: true, recursive: true})) {
    if (entry.isFile() && entry.name.endsWith('.html')) {
      pagePaths.push(relative(root, join(entry.parentPath, entry.name)).split(sep).join('/'));
    }
  }
  return pagePaths.sort();
}

async function isDirectory(path) {
  try {
    return (await stat(path)).isDirectory();
  } catch (error) {
    if (error.code === 'ENOENT') return false;
    throw error;
  }
}

export function minorOf(version) {
  return version.split('.').slice(0, 2).join('.');
}
