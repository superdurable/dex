// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

const apiReferencePagePattern = /^\/api\/([^/]+)\/(\d+\.\d+)\/(.*)$/;

/**
 * Returns where the 404 page sends a URL under a retired API reference version.
 *
 * Assembly stops publishing a version once it leaves the manifest, so its pages reach the 404 page.
 *
 * @param {{pathname: string, search: string, hash: string}} location The missing page's URL parts.
 * @param {Record<string, unknown>} releasesByLanguage The parsed api-reference-versions.json.
 * @param {(pathname: string) => Promise<boolean>} isPagePublished Resolves true when the site serves the path.
 * @returns {Promise<string | undefined>} The same page under `/api/<language>/latest/` when it exists, otherwise
 *   `/reference/<language>/`, both keeping the query and fragment. Undefined when the URL is not under a retired
 *   minor of a manifest language, including a missing page under a current version or `latest`.
 */
export async function retiredApiReferenceRedirectTarget(location, releasesByLanguage, isPagePublished) {
  const match = apiReferencePagePattern.exec(location.pathname);
  if (!match) return undefined;
  const [, language, minor, pagePath] = match;
  const releases = Object.hasOwn(releasesByLanguage, language) ? releasesByLanguage[language] : undefined;
  if (!Array.isArray(releases) || releases.some((release) => String(release).startsWith(`${minor}.`))) return undefined;
  const querySuffix = `${location.search}${location.hash}`;
  const latestPagePath = `/api/${language}/latest/${pagePath}`;
  return (await isPagePublished(latestPagePath)) ? `${latestPagePath}${querySuffix}` : `/reference/${language}/${querySuffix}`;
}
