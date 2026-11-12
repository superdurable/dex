// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

const defaultPorts: Readonly<Record<string, number>> = { http: 80, https: 443 };
const urlParts = /^([A-Za-z][A-Za-z0-9+.-]*):\/\/([^/?#]*)([^?#]*)/;
const authorityPattern = /^(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?)(?::([^:]*))?$/;
const portPattern = /^[1-9][0-9]{0,4}$/;

interface UrlOrigin {
  readonly scheme: string;
  readonly host: string;
  readonly port: number;
}

/**
 * Returns whether `open_url` may open a URL: an `http` or `https` URL of printable ASCII, with the scheme, host,
 * and port of `webUrl`, no userinfo, no dot segments, and a path under `webUrl`'s path. A `null` `webUrl` refuses
 * every URL. The rules are those of the terminal UI contract, and `open_url.json` holds their cases.
 */
export function isOpenUrlAllowed(webUrl: string | null, url: string): boolean {
  if (webUrl === null || !hasOnlyPlainCharacters(url)) {
    return false;
  }
  const base = urlOrigin(webUrl);
  const target = urlOrigin(url);
  if (base === undefined || target === undefined) {
    return false;
  }
  if (base.scheme !== target.scheme || base.host !== target.host || base.port !== target.port) {
    return false;
  }
  const targetPath = urlParts.exec(url)?.[3] ?? "";
  if (targetPath.split("/").some((segment) => isDotSegment(segment))) {
    return false;
  }
  const basePath = (urlParts.exec(webUrl)?.[3] ?? "").replace(/\/+$/, "");
  return basePath === "" || targetPath === basePath || targetPath.startsWith(`${basePath}/`);
}

function hasOnlyPlainCharacters(url: string): boolean {
  for (const character of url) {
    const codePoint = character.codePointAt(0) ?? 0;
    if (codePoint <= 0x20 || codePoint >= 0x7f || character === "\\") {
      return false;
    }
  }
  return true;
}

function urlOrigin(url: string): UrlOrigin | undefined {
  if (!hasOnlyPlainCharacters(url)) {
    return undefined;
  }
  const parts = urlParts.exec(url);
  if (parts === null) {
    return undefined;
  }
  const scheme = (parts[1] ?? "").toLowerCase();
  const authority = parts[2] ?? "";
  const defaultPort = defaultPorts[scheme];
  if (defaultPort === undefined || authority.includes("@") || authority.includes("%")) {
    return undefined;
  }
  const match = authorityPattern.exec(authority);
  if (match === null) {
    return undefined;
  }
  const host = (match[1] ?? "").toLowerCase();
  const portText = match[2];
  if (portText === undefined) {
    return { scheme, host, port: defaultPort };
  }
  if (!portPattern.test(portText) || Number(portText) > 65535) {
    return undefined;
  }
  return { scheme, host, port: Number(portText) };
}

function isDotSegment(segment: string): boolean {
  const decoded = segment.toLowerCase().replaceAll("%2e", ".");
  return decoded === "." || decoded === "..";
}
