// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

export function TimeTravelEventAction({ onClick }: { onClick: () => void }) {
  return (
    <div
      aria-label="Critical action: time travel from selected event"
      className="selected-event-time-travel"
      role="group"
    >
      <div className="selected-event-time-travel-copy">
        <WarningIcon />
        <div>
          <strong>Critical action</strong>
          <span>Creates a new run from this event.</span>
        </div>
      </div>
      <button className="button danger" onClick={onClick} type="button">
        Review time travel
      </button>
    </div>
  );
}

function WarningIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 3 2.8 20h18.4L12 3Z" />
      <path d="M12 9v5" />
      <path d="M12 17.5v.1" />
    </svg>
  );
}
