// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

export const VALUE_BLOB_UNAVAILABLE = 'Value blob unavailable';
export const ASYNC_STEP_INPUT_SNAPSHOT_NOT_RECORDED =
  'This ASYNC Step method exhausted its short retry policy before sync fallback, so its full invocation input snapshot was not recorded. This does not indicate an individual Value blob load failure.';
export const STEP_INPUT_SNAPSHOT_UNAVAILABLE =
  'The Server may have set blobStore.asyncStepInputSnapshotsEnabled to false, Blob Store may be disabled, or this snapshot may no longer be retained. The full invocation input snapshot is unavailable; this does not indicate an individual Value blob load failure.';
