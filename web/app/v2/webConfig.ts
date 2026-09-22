// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

export type WorkQueuePermissionMode = 'local-selector' | 'trusted-header';

declare global {
  interface Window {
    __DEX_WEB_CONFIG__?: {
      workQueuePermissionMode?: WorkQueuePermissionMode;
    };
  }
}

export function workQueuePermissionMode(): WorkQueuePermissionMode {
  return window.__DEX_WEB_CONFIG__?.workQueuePermissionMode ?? 'local-selector';
}

export function definitionRevisionHeaders(revision: string): HeadersInit {
  return revision === '' ? {} : { 'X-Dex-Flow-Definition-Revision': revision };
}
