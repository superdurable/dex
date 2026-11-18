// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { TimelineLabelMode } from './timelineOverview';

export const timelineLabelModeStorageKey = 'dex.run.timeline-label-mode';
export const timelineLabelModeIntroStorageKey = 'dex.run.timeline-label-mode-intro-dismissed';

/** The label mode this browser last chose for the Timeline; Tidy when none is saved or storage fails. */
export function storedTimelineLabelMode(): TimelineLabelMode {
  if (typeof window === 'undefined') return 'tidy';
  try {
    return window.localStorage.getItem(timelineLabelModeStorageKey) === 'dense' ? 'dense' : 'tidy';
  } catch (storageError) {
    console.warn('Unable to read the Timeline label mode preference.', storageError);
    return 'tidy';
  }
}

export function persistTimelineLabelMode(labelMode: TimelineLabelMode) {
  try {
    window.localStorage.setItem(timelineLabelModeStorageKey, labelMode);
  } catch (storageError) {
    console.warn('Unable to save the Timeline label mode preference.', storageError);
  }
}

/** Whether this browser should show the label mode intro: not dismissed yet, and storage can remember it. */
export function shouldShowTimelineLabelModeIntro(): boolean {
  if (typeof window === 'undefined') return false;
  try {
    return window.localStorage.getItem(timelineLabelModeIntroStorageKey) !== 'true';
  } catch (storageError) {
    // Without storage the intro could not stay dismissed, so it never shows.
    console.warn('Unable to read the Timeline label mode intro preference.', storageError);
    return false;
  }
}

export function persistTimelineLabelModeIntroDismissed() {
  try {
    window.localStorage.setItem(timelineLabelModeIntroStorageKey, 'true');
  } catch (storageError) {
    console.warn('Unable to save the Timeline label mode intro preference.', storageError);
  }
}
