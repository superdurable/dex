// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

export const HOST_NAVIGATION_META_NAME = 'dex-web-host-navigation';

/** Returns whether the page that embeds this Dex Web draws the navigation around it, so Dex Web draws no rail. */
export function readHasHostNavigation(): boolean {
  if (typeof document === 'undefined') return false;
  return document.querySelector(`meta[name="${HOST_NAVIGATION_META_NAME}"]`)?.getAttribute('content')?.trim() === 'true';
}
