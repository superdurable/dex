/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode, useEffect, useState} from 'react';
import {translate} from '@docusaurus/Translate';

const dexRepositoryApiUrl = 'https://api.github.com/repos/superdurable/dex';
const starCountCacheKey = 'superdurable-dex-star-count';
// Unauthenticated GitHub API calls are limited to 60 per hour per IP.
const starCountCacheMilliseconds = 60 * 60 * 1000;
const failedFetchCacheMilliseconds = 10 * 60 * 1000;
const fetchTimeoutMilliseconds = 5000;

type CachedStarCount = {starCount: number | null; fetchedAt: number};

// The header's GitHub pill. The stars segment appears only after the browser knows the count.
export default function DexRepositoryGitHubLink({href, label}: {href: string; label: ReactNode}): ReactNode {
  const starCount = useDexRepositoryStarCount();
  const formattedStarCount = starCount === null ? null : formatStarCount(starCount);
  return (
    <a
      className="sd-github-link"
      href={href}
      aria-label={formattedStarCount === null ? undefined : starCountAriaLabel(starCount, formattedStarCount)}>
      <span className="sd-github-link-name">
        <svg aria-hidden="true" focusable="false" viewBox="0 0 16 16" width="16" height="16">
          <path
            fill="currentColor"
            d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0 0 16 8c0-4.42-3.58-8-8-8Z"
          />
        </svg>
        {label}
      </span>
      {formattedStarCount === null ? null : (
        <span className="sd-github-link-stars">
          <svg aria-hidden="true" focusable="false" viewBox="0 0 16 16" width="14" height="14">
            <path
              fill="currentColor"
              d="M8 .25a.75.75 0 0 1 .673.418l1.882 3.815 4.21.612a.75.75 0 0 1 .416 1.279l-3.046 2.97.719 4.192a.751.751 0 0 1-1.088.791L8 12.347l-3.766 1.98a.75.75 0 0 1-1.088-.79l.72-4.194L.818 6.374a.75.75 0 0 1 .416-1.28l4.21-.611L7.327.668A.75.75 0 0 1 8 .25Z"
            />
          </svg>
          {formattedStarCount}
        </span>
      )}
    </a>
  );
}

function starCountAriaLabel(starCount: number | null, formattedStarCount: string): string {
  if (starCount === 1) {
    return translate(
      {
        id: 'shell.header.githubOneStarAriaLabel',
        message: 'GitHub, {starCount} star',
        description: 'The accessible name of the header GitHub link when the repository has one star',
      },
      {starCount: formattedStarCount},
    );
  }
  return translate(
    {
      id: 'shell.header.githubStarsAriaLabel',
      message: 'GitHub, {starCount} stars',
      description: 'The accessible name of the header GitHub link with the repository star count as shown',
    },
    {starCount: formattedStarCount},
  );
}

// Reads the cache and fetches only in an effect, so the static HTML never holds a count.
function useDexRepositoryStarCount(): number | null {
  const [starCount, setStarCount] = useState<number | null>(null);

  useEffect(() => {
    const cachedStarCount = readCachedStarCount();
    if (cachedStarCount !== undefined) {
      setStarCount(cachedStarCount.starCount);
      return undefined;
    }
    const controller = new AbortController();
    const timeoutId = window.setTimeout(() => controller.abort(), fetchTimeoutMilliseconds);
    fetchStarCount(controller.signal)
      .then((fetchedStarCount) => {
        writeCachedStarCount(fetchedStarCount);
        setStarCount(fetchedStarCount);
      })
      .catch((error: unknown) => {
        // An abort is a timeout or an unmount, not a GitHub failure, so it is not cached.
        if (!controller.signal.aborted) {
          console.warn('Could not fetch the Dex repository star count', error);
          writeCachedStarCount(null);
        }
      })
      .finally(() => window.clearTimeout(timeoutId));
    return () => {
      window.clearTimeout(timeoutId);
      controller.abort();
    };
  }, []);

  return starCount;
}

async function fetchStarCount(signal: AbortSignal): Promise<number> {
  const response = await fetch(dexRepositoryApiUrl, {signal, headers: {Accept: 'application/vnd.github+json'}});
  if (!response.ok) {
    throw new Error(`GitHub API returned HTTP ${response.status}`);
  }
  const repository = (await response.json()) as {stargazers_count?: unknown} | null;
  const fetchedStarCount = repository?.stargazers_count;
  if (!isStarCount(fetchedStarCount)) {
    throw new Error('GitHub API response has no valid stargazers_count');
  }
  return fetchedStarCount;
}

// Returns the fresh cached entry, whose starCount is null for a recent failure, or undefined when none is usable.
function readCachedStarCount(): CachedStarCount | undefined {
  let cached: {starCount?: unknown; fetchedAt?: unknown} | null;
  try {
    cached = JSON.parse(window.localStorage.getItem(starCountCacheKey) ?? 'null');
  } catch {
    return undefined;
  }
  if (cached === null || typeof cached !== 'object' || typeof cached.fetchedAt !== 'number') {
    return undefined;
  }
  const cacheAgeMilliseconds = Date.now() - cached.fetchedAt;
  if (cacheAgeMilliseconds < 0) {
    return undefined;
  }
  if (cached.starCount === null) {
    return cacheAgeMilliseconds < failedFetchCacheMilliseconds ? {starCount: null, fetchedAt: cached.fetchedAt} : undefined;
  }
  if (isStarCount(cached.starCount) && cacheAgeMilliseconds < starCountCacheMilliseconds) {
    return {starCount: cached.starCount, fetchedAt: cached.fetchedAt};
  }
  return undefined;
}

function writeCachedStarCount(starCount: number | null): void {
  const cachedStarCount: CachedStarCount = {starCount, fetchedAt: Date.now()};
  try {
    window.localStorage.setItem(starCountCacheKey, JSON.stringify(cachedStarCount));
  } catch {
    // Storage may be blocked; the next page fetches again.
  }
}

function isStarCount(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

// Formats counts of 1000 or more in tenths of a thousand, such as 1.2k.
function formatStarCount(starCount: number): string {
  return starCount >= 1000 ? `${Math.round(starCount / 100) / 10}k` : String(starCount);
}
