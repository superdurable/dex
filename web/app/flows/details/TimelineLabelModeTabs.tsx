// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useState } from 'react';
import { Italic, WrapText, X, type LucideIcon } from 'lucide-react';
import type { TimelineLabelMode } from './timelineOverview';
import {
  persistTimelineLabelModeIntroDismissed,
  shouldShowTimelineLabelModeIntro,
} from './timelineLabelModeStorage';

const labelModeChoices: { mode: TimelineLabelMode; label: string; icon: LucideIcon; intro: string }[] = [
  { mode: 'tidy', label: 'Tidy', icon: WrapText, intro: 'Straight labels in columns, wrapped to three lines.' },
  { mode: 'dense', label: 'Dense', icon: Italic, intro: 'Slanted labels, so more events fit in view.' },
];

/** Whether the label mode intro shows, and how to dismiss it for good. */
export function useTimelineLabelModeIntro(): { isIntroShown: boolean; dismissIntro: () => void } {
  const [isIntroShown, setIsIntroShown] = useState(shouldShowTimelineLabelModeIntro);
  const dismissIntro = useCallback(() => {
    setIsIntroShown(false);
    persistTimelineLabelModeIntroDismissed();
  }, []);
  return { isIntroShown, dismissIntro };
}

/** Switches the Timeline between Tidy and Dense labels. */
export function TimelineLabelModeTabs({
  labelMode,
  onChangeLabelMode,
  introId,
}: {
  labelMode: TimelineLabelMode;
  onChangeLabelMode: (labelMode: TimelineLabelMode) => void;
  /** The intro's element ID while it shows, so the tabs point to it. */
  introId?: string;
}) {
  return (
    <div aria-describedby={introId} aria-label="Timeline labels" className="htl-mode-tabs" role="group">
      {labelModeChoices.map(({ mode, label, icon: Icon }) => (
        <button
          aria-pressed={labelMode === mode}
          className="htl-mode-tab"
          key={mode}
          onClick={() => onChangeLabelMode(mode)}
          type="button"
        >
          <Icon aria-hidden="true" size={12} strokeWidth={2.25} />
          {label}
        </button>
      ))}
    </div>
  );
}

/** A one-time note on the two label modes, under the head row so it covers no event. */
export function TimelineLabelModeIntro({ id, onDismiss }: { id: string; onDismiss: () => void }) {
  return (
    <div aria-label="Timeline label modes" className="htl-mode-intro" id={id} role="note">
      <ul>
        {labelModeChoices.map(({ mode, label, intro }) => (
          <li key={mode}><strong>{label}</strong> {intro}</li>
        ))}
      </ul>
      <button aria-label="Dismiss" className="htl-mode-intro-dismiss" onClick={onDismiss} title="Dismiss" type="button">
        <X aria-hidden="true" size={14} strokeWidth={2.25} />
      </button>
    </div>
  );
}
