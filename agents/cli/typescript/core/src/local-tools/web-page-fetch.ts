// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { ToolExecutionError } from "@superdurable/dex-agents";

const fetchTimeoutMs = 30_000;
const maximumBodyBytes = 1024 * 1024;
const maximumTextCharacters = 100_000;

/**
 * Fetches an `http` or `https` URL and returns its text; HTML becomes plain text.
 *
 * @param url - The URL.
 * @param signal - Aborts the request.
 * @returns The status, the content type, and at most 100,000 characters of text.
 * @throws {ToolExecutionError} For another scheme or a request that fails.
 */
export async function fetchWebPage(url: string, signal: AbortSignal): Promise<string> {
  const target = parseHttpUrl(url);
  let response: Response;
  try {
    response = await fetch(target, {
      redirect: "follow",
      signal: AbortSignal.any([signal, AbortSignal.timeout(fetchTimeoutMs)]),
      headers: { "user-agent": "dex-agents-cli" },
    });
  } catch (error: unknown) {
    throw new ToolExecutionError(`fetching ${url} failed: ${error instanceof Error ? error.message : String(error)}`, {
      cause: error,
    });
  }
  const contentType = response.headers.get("content-type") ?? "";
  const body = await readCappedBody(response);
  const text = /html/i.test(contentType) ? convertHtmlToText(body) : body;
  const cut = text.length > maximumTextCharacters ? `${text.slice(0, maximumTextCharacters)}\n[cut]` : text;
  return `HTTP ${response.status} ${contentType}\n\n${cut}`;
}

/**
 * Returns a URL when it is `http` or `https`.
 * @param url - The URL text.
 * @returns The parsed URL.
 * @throws {ToolExecutionError} For an invalid URL or another scheme.
 */
export function parseHttpUrl(url: string): URL {
  let target: URL;
  try {
    target = new URL(url);
  } catch (error: unknown) {
    throw new ToolExecutionError(`${url} is not a URL`, { cause: error });
  }
  if (target.protocol !== "http:" && target.protocol !== "https:") {
    throw new ToolExecutionError(`only http and https URLs are allowed, not ${target.protocol}`);
  }
  return target;
}

async function readCappedBody(response: Response): Promise<string> {
  if (response.body === null) {
    return "";
  }
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let byteCount = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) {
      break;
    }
    chunks.push(value);
    byteCount += value.length;
    if (byteCount >= maximumBodyBytes) {
      await reader.cancel();
      break;
    }
  }
  return Buffer.concat(chunks).subarray(0, maximumBodyBytes).toString("utf8");
}

/** A small HTML-to-text conversion: drops scripts, styles, and tags, and decodes common entities. */
function convertHtmlToText(html: string): string {
  return html
    .replace(/<(script|style|noscript)\b[\s\S]*?<\/\1\s*>/gi, " ")
    .replace(/<br\s*\/?>/gi, "\n")
    .replace(/<\/(p|div|li|h[1-6]|tr|section|article)>/gi, "\n")
    .replace(/<[^>]+>/g, " ")
    .replace(/&nbsp;/g, " ")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, "&")
    .replace(/[ \t]+/g, " ")
    .replace(/\n\s*\n+/g, "\n\n")
    .trim();
}
