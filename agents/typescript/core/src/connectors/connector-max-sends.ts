// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Send cap bounds shared with the Connector SDK, kept here so the session never imports the optional SDK.

/** The largest send cap a manifest or a tool policy may set, as the Connector SDK's `MaximumMaxSends`. */
export const connectorMaxSendsCeiling = 10;

/** The smallest `agent.maxSends` a manifest may declare; a tool policy may also set 1. */
export const connectorManifestMinimumMaxSends = 2;

/** The send cap of a `limited` operation definition that omits `maxSends`, as the SDK's `DefaultLimitedMaxSends`. */
export const connectorDefaultLimitedMaxSends = 3;

/** Reports whether a value is an integer send cap from 1 to {@link connectorMaxSendsCeiling}. */
export function isConnectorMaxSends(value: unknown): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= 1 && value <= connectorMaxSendsCeiling;
}
