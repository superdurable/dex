// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

/**
 * Returns the URL path of the Markdown export for a docs page.
 *
 * The exporter, the robot link, and the site audit share this mapping.
 *
 * @param {string} permalink The page route, such as `/primitives/attribute/`.
 * @param {string} baseUrl The locale base URL, such as `/` or `/zh-Hans/`.
 * @returns {string} The sibling `.md` path, or `<baseUrl>index.md` for the locale home page.
 */
export function markdownPath(permalink, baseUrl) {
  const route = permalink.replace(/\/+$/, '');
  return route === baseUrl.replace(/\/+$/, '') ? `${baseUrl}index.md` : `${route}.md`;
}
