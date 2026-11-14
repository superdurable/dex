// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ServerInfo } from "./gen/dex.js";

export const minimumSupportedServerProtocolVersion: number = 4;
export const maximumSupportedServerProtocolVersion: number = 5;
const minimumServerRelease = "server-zd newer than v0.1.16";

export function negotiateServerProtocol(
  serverInfo: ServerInfo,
  sdkVersion: string,
): number {
  const serverMinimum = serverInfo.minimumSupportedProtocolVersion;
  const serverCurrent = serverInfo.currentProtocolVersion;
  if (
    minimumSupportedServerProtocolVersion === 0
    || maximumSupportedServerProtocolVersion === 0
    || minimumSupportedServerProtocolVersion > maximumSupportedServerProtocolVersion
  ) {
    throw new Error(`${sdkDescription(sdkVersion)} has an invalid protocol range.`);
  }
  if (serverMinimum === 0 || serverCurrent === 0 || serverMinimum > serverCurrent) {
    throw new Error(
      `${sdkDescription(sdkVersion)} cannot use the Server: GetServerInfo returned an empty or invalid `
        + `protocol range. ${serverDescription(serverInfo)}`,
    );
  }
  if (serverCurrent < minimumSupportedServerProtocolVersion) {
    throw new Error(
      `${sdkDescription(sdkVersion)} is incompatible with the Server. `
        + `This SDK requires a Dex Server with protocol version ${minimumSupportedServerProtocolVersion} or later `
        + `(${minimumServerRelease}). ${serverDescription(serverInfo)} Upgrade the Server before upgrading the SDK.`,
    );
  }
  if (serverMinimum > maximumSupportedServerProtocolVersion) {
    throw new Error(
      `${sdkDescription(sdkVersion)} is incompatible with the Server. `
        + `The Server requires protocol version ${serverMinimum} or later. ${serverDescription(serverInfo)} `
        + "Upgrade the TypeScript SDK.",
    );
  }
  return Math.min(serverCurrent, maximumSupportedServerProtocolVersion);
}

function sdkDescription(sdkVersion: string): string {
  return `TypeScript SDK version "${sdkVersion}" protocol `
    + `[${minimumSupportedServerProtocolVersion},${maximumSupportedServerProtocolVersion}]`;
}

function serverDescription(serverInfo: ServerInfo): string {
  return `The Server reports protocol versions ${serverInfo.minimumSupportedProtocolVersion}-${serverInfo.currentProtocolVersion} `
    + `(server version ${serverInfo.serverVersion || "unknown"}).`;
}
