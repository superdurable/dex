/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import type { IncomingMessage, ServerResponse } from "node:http";

/** The outcome of the CORS check for one request. */
export type CorsDecision = "continue" | "forbidden" | "preflight-answered";

/** Answers CORS for the configured Dex Web origins only. */
export class CorsOriginAllowList {
  private readonly origins: ReadonlySet<string>;
  private readonly allowCredentials: boolean;

  public constructor(origins: readonly string[], allowCredentials: boolean) {
    this.origins = new Set(origins.map((origin) => origin.toLowerCase()));
    this.allowCredentials = allowCredentials;
  }

  /**
   * Runs before authentication, because preflights carry no credentials.
   * Requests without Origin come from non-browser clients and continue.
   * @returns "forbidden" for an unlisted origin; "preflight-answered" after writing a 204.
   */
  public applyCorsHeaders(request: IncomingMessage, response: ServerResponse): CorsDecision {
    const origin = request.headers.origin;
    if (origin === undefined) {
      return "continue";
    }
    response.appendHeader("Vary", "Origin");
    if (!this.origins.has(origin.toLowerCase())) {
      return "forbidden";
    }
    response.setHeader("Access-Control-Allow-Origin", origin);
    if (this.allowCredentials) {
      response.setHeader("Access-Control-Allow-Credentials", "true");
    }
    if (request.method === "OPTIONS" && request.headers["access-control-request-method"] !== undefined) {
      response.setHeader("Access-Control-Allow-Methods", "POST");
      response.setHeader("Access-Control-Allow-Headers", "Authorization, Content-Type");
      response.setHeader("Access-Control-Max-Age", "600");
      response.writeHead(204);
      response.end();
      return "preflight-answered";
    }
    return "continue";
  }
}
