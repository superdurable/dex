// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";

/** Exported Dex credential variables that no tool may see. */
export const secretSentinels = {
  DEX_API_KEY: "dex-api-key-sentinel-1",
  DEX_CREDENTIAL: "dex-credential-sentinel-2",
  DEX_CREDENTIAL_FILE: "/run/dex-credential-file-sentinel-3",
} as const;

/** Fails when a shell environment holds a Dex credential variable or a sentinel value. */
export function assertNoDexSecret(shellEnvironment: string): void {
  for (const [name, value] of Object.entries(secretSentinels)) {
    assert.ok(!shellEnvironment.includes(`${name}=`), name);
    assert.ok(!shellEnvironment.includes(value), name);
  }
}
