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

/** The `type` of the message that tells the host page which Dex Web page the frame shows. */
export const HOST_LOCATION_MESSAGE_TYPE = 'dex-web:location';

/** The version of the host location message shape. A breaking change to the shape raises it. */
export const HOST_LOCATION_MESSAGE_VERSION = 1;

/** The message Dex Web posts to its parent window after each route change under host navigation. */
export interface HostLocationMessage {
  type: typeof HOST_LOCATION_MESSAGE_TYPE;
  version: typeof HOST_LOCATION_MESSAGE_VERSION;
  /** The path relative to the base path, such as `/v1/flows/FLOW_ID/RUN_ID`. */
  path: string;
  /** The query string with its leading `?`, or empty. */
  search: string;
}

/**
 * Posts the current location to the parent window when Dex Web is framed under host navigation.
 * The target origin is this page's own origin, because the embedding host serves Dex Web on its origin.
 */
export function postHostLocation(path: string, search: string): void {
  if (typeof window === 'undefined' || window.parent === window) return;
  const message: HostLocationMessage = {
    type: HOST_LOCATION_MESSAGE_TYPE,
    version: HOST_LOCATION_MESSAGE_VERSION,
    path,
    search,
  };
  window.parent.postMessage(message, window.location.origin);
}
