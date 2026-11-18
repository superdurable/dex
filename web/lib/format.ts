// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

export type TimezonePreference = 'local' | 'UTC';

// Building a formatter is costly, and a Timeline page formats up to 1000 dates per render.
const dateFormatters = new Map<TimezonePreference, Intl.DateTimeFormat>();

export function formatDate(value: string | null, timezone: TimezonePreference): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  let formatter = dateFormatters.get(timezone);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat(undefined, {
      dateStyle: 'medium',
      timeStyle: 'medium',
      timeZone: timezone === 'UTC' ? 'UTC' : undefined,
    });
    dateFormatters.set(timezone, formatter);
  }
  return formatter.format(date);
}

// Seconds with milliseconds tell apart events that start in the same second.
const millisecondTimeFormatters = new Map<TimezonePreference, Intl.DateTimeFormat>();

/** Time of day with milliseconds, such as "10:24:17.302 PM"; the event's header carries the date. */
export function formatTimeWithMilliseconds(value: string | null, timezone: TimezonePreference): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  let formatter = millisecondTimeFormatters.get(timezone);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat(undefined, {
      hour: 'numeric',
      minute: '2-digit',
      second: '2-digit',
      fractionalSecondDigits: 3,
      timeZone: timezone === 'UTC' ? 'UTC' : undefined,
    });
    millisecondTimeFormatters.set(timezone, formatter);
  }
  return formatter.format(date);
}

/** Reports whether two instants fall on the same calendar day in the viewer's timezone. */
export function isSameCalendarDay(startMs: number, endMs: number, timezone: TimezonePreference): boolean {
  if (timezone === 'UTC') return new Date(startMs).toISOString().slice(0, 10) === new Date(endMs).toISOString().slice(0, 10);
  return new Date(startMs).toDateString() === new Date(endMs).toDateString();
}

const monthDayFormatters = new Map<TimezonePreference, Intl.DateTimeFormat>();

/**
 * Time with milliseconds, such as "10:24:17.302 PM", led by the month and day, such as "Nov 16",
 * when it falls on another calendar day than referenceTime.
 */
export function formatTimeBesideReference(value: string, referenceTime: string | null, timezone: TimezonePreference): string {
  const timeOfDay = formatTimeWithMilliseconds(value, timezone);
  const valueMs = Date.parse(value);
  const referenceMs = referenceTime ? Date.parse(referenceTime) : Number.NaN;
  if (Number.isNaN(valueMs) || Number.isNaN(referenceMs) || isSameCalendarDay(valueMs, referenceMs, timezone)) return timeOfDay;
  let formatter = monthDayFormatters.get(timezone);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat(undefined, {
      month: 'short',
      day: 'numeric',
      timeZone: timezone === 'UTC' ? 'UTC' : undefined,
    });
    monthDayFormatters.set(timezone, formatter);
  }
  return `${formatter.format(valueMs)}, ${timeOfDay}`;
}

/** Time only, for a narrow column where the date would not fit and rarely differs. */
export function formatTimeOfDay(value: string | null, timezone: TimezonePreference): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  return new Intl.DateTimeFormat(undefined, {
    timeStyle: 'short',
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
