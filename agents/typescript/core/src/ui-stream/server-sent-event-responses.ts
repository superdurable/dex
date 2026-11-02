// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** SSE comment sent after an empty long poll; it keeps proxies from closing an idle response. */
export const serverSentEventKeepAliveFrame = ": keep-alive\n\n";

/** Returns a streaming Response that writes each frame as it is produced and stops the frames on cancel. */
export function createServerSentEventResponse(
  frames: AsyncGenerator<string, void, undefined>,
  baseHeaders: Readonly<Record<string, string>>,
  extraHeaders: Readonly<Record<string, string>> | undefined,
): Response {
  const encoder = new TextEncoder();
  const body = new ReadableStream<Uint8Array>({
    async pull(controller) {
      const frame = await frames.next();
      if (frame.done === true) {
        controller.close();
        return;
      }
      controller.enqueue(encoder.encode(frame.value));
    },
    async cancel() {
      await frames.return(undefined);
    },
  });
  return new Response(body, { status: 200, headers: mergeHeaders(baseHeaders, extraHeaders) });
}

/** Returns the base headers overridden by the application's headers. */
export function mergeHeaders(
  baseHeaders: Readonly<Record<string, string>>,
  extraHeaders: Readonly<Record<string, string>> | undefined,
): Headers {
  const headers = new Headers(baseHeaders);
  new Headers(extraHeaders).forEach((value, name) => headers.set(name, value));
  return headers;
}
