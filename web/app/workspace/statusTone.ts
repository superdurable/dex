// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

export type StatusTone = 'attention' | 'success' | 'danger' | 'neutral';

/** A status chip's colour, from common words in its value; anything else stays neutral. */
export function statusTone(text: string): StatusTone {
  const value = text.toLowerCase();
  if (/(fail|error|reject|denied|cancel)/.test(value)) return 'danger';
  if (/(await|wait|pending|review|approval|blocked)/.test(value)) return 'attention';
  if (/(done|complete|approved|success|resolved|closed)/.test(value)) return 'success';
  return 'neutral';
}
