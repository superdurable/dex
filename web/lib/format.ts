// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

export type TimezonePreference = 'local' | 'UTC';

export function formatDate(value: string | null, timezone: TimezonePreference): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'medium',
    timeZone: timezone === 'UTC' ? 'UTC' : undefined,
  }).format(date);
}

export function formatDuration(start: string | null, close: string | null): string {
  if (!start) return '—';
  const startMs = Date.parse(start);
  const endMs = close ? Date.parse(close) : Date.now();
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs)) return '—';
  const seconds = Math.max(0, Math.floor((endMs - startMs) / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m`;
}

export function displayValue(value: unknown): string {
  if (value === null || value === undefined || value === '') return '—';
  if (typeof value === 'string') return value;
  return JSON.stringify(value, null, 2);
}
