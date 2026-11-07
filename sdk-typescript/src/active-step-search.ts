// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { ActiveStepSearchMode as ProtoActiveStepSearchMode } from "./gen/dex.js";
import { ActiveStepSearchMode, type FlowConfig } from "./options.js";

export function mapActiveStepSearchMode(
  config: FlowConfig | undefined,
): ProtoActiveStepSearchMode | undefined {
  switch (config?.activeStepSearchMode) {
    case undefined:
      return undefined;
    case ActiveStepSearchMode.ALL:
      return ProtoActiveStepSearchMode.ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL;
    case ActiveStepSearchMode.WITH_WAIT_FOR:
      return ProtoActiveStepSearchMode.ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR;
    case ActiveStepSearchMode.DISABLED:
      return ProtoActiveStepSearchMode.ACTIVE_STEP_SEARCH_MODE_DISABLED;
    case ActiveStepSearchMode.DEFAULT:
      return ProtoActiveStepSearchMode.ACTIVE_STEP_SEARCH_MODE_UNSPECIFIED;
  }
}
