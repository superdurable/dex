// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useId, useState } from 'react';
import { Italic, WrapText, X, type LucideIcon } from 'lucide-react';
import type { TimelineLabelMode } from './timelineOverview';
import {
  persistTimelineLabelModeIntroDismissed,
  shouldShowTimelineLabelModeIntro,
} from './timelineLabelModeStorage';

const labelModeChoices: { mode: TimelineLabelMode; label: string; icon: LucideIcon; intro: string }[] = [
  { mode: 'dense', label: 'Dense', icon: Italic, intro: 'Slanted labels, so more events fit in view.' },
  { mode: 'tidy', label: 'Tidy', icon: WrapText, intro: 'Straight labels in columns, wrapped to three lines.' },
];

/** Switches the Timeline between Dense and Tidy labels; the first visit also explains both once. */
export function TimelineLabelModeTabs({
  labelMode,
  onChangeLabelMode,
}: {
  labelMode: TimelineLabelMode;
  onChangeLabelMode: (labelMode: TimelineLabelMode) => void;
}) {
  const [isIntroShown, setIsIntroShown] = useState(shouldShowTimelineLabelModeIntro);
  const introId = useId();

  const dismissIntro = () => {
    setIsIntroShown(false);
    persistTimelineLabelModeIntroDismissed();
  };

  // Choosing a mode shows the reader has found the tabs, so the intro is done.
  const chooseLabelMode = (nextLabelMode: TimelineLabelMode) => {
    if (isIntroShown) dismissIntro();
    onChangeLabelMode(nextLabelMode);
  };

  return (
    <div className="htl-mode">
      <div
        aria-describedby={isIntroShown ? introId : undefined}
        aria-label="Timeline labels"
        className="htl-mode-tabs"
        role="group"
      >
        {labelModeChoices.map(({ mode, label, icon: Icon }) => (
          <button
            aria-pressed={labelMode === mode}
            className="htl-mode-tab"
            key={mode}
            onClick={() => chooseLabelMode(mode)}
            type="button"
          >
            <Icon aria-hidden="true" size={13} strokeWidth={2.25} />
            {label}
          </button>
        ))}
      </div>
      {isIntroShown && (
        <div aria-label="Timeline label modes" className="htl-mode-intro" id={introId} role="note">
          <ul>
            {labelModeChoices.map(({ mode, label, intro }) => (
              <li key={mode}><strong>{label}</strong> {intro}</li>
            ))}
          </ul>
          <button aria-label="Dismiss" className="htl-mode-intro-dismiss" onClick={dismissIntro} title="Dismiss" type="button">
            <X aria-hidden="true" size={14} strokeWidth={2.25} />
          </button>
        </div>
      )}
    </div>
  );
}
