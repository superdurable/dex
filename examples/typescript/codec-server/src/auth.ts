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

import { createHash, timingSafeEqual } from "node:crypto";
import type { IncomingMessage } from "node:http";

import type { PayloadCodec } from "@superdurable/dex";

import { authModeBearerToken, authModeTrustedProxyHeader, type CodecServerConfig } from "./config.ts";

/** Who an authenticator proved the caller to be. */
export interface CallerIdentity {
  readonly subject: string;
}

/**
 * Authenticates every /encode and /decode request. Implement it for your identity provider.
 * Returning undefined answers 401.
 */
export interface RequestAuthenticator {
  authenticateRequest(request: IncomingMessage): CallerIdentity | undefined;
}

/** Accepts one shared secret in `Authorization: Bearer <token>`. */
export class BearerTokenRequestAuthenticator implements RequestAuthenticator {
  private readonly tokenDigest: Buffer;

  public constructor(token: string) {
    this.tokenDigest = sha256(token);
  }

  public authenticateRequest(request: IncomingMessage): CallerIdentity | undefined {
    const authorization = request.headers.authorization ?? "";
    const separator = authorization.indexOf(" ");
    if (separator < 0 || authorization.slice(0, separator).toLowerCase() !== "bearer") {
      return undefined;
    }
    const token = authorization.slice(separator + 1);
    if (token === "") {
      return undefined;
    }
    // Comparing fixed-size digests keeps the comparison constant-time for any token length.
    if (!timingSafeEqual(sha256(token), this.tokenDigest)) {
      return undefined;
    }
    return { subject: "bearer-token" };
  }
}

/** Trusts a user header set by an SSO proxy. Safe only when that proxy is the sole path in. */
export class TrustedProxyHeaderRequestAuthenticator implements RequestAuthenticator {
  private readonly headerName: string;

  public constructor(headerName: string) {
    this.headerName = headerName.toLowerCase();
  }

  public authenticateRequest(request: IncomingMessage): CallerIdentity | undefined {
    // headersDistinct keeps duplicates apart; request.headers would join them with commas.
    const values = request.headersDistinct[this.headerName];
    if (values === undefined || values.length !== 1) {
      return undefined;
    }
    const subject = values[0]!.trim();
    return subject === "" ? undefined : { subject };
  }
}

/** @returns The authenticator for config.authMode. */
export function createRequestAuthenticator(config: CodecServerConfig): RequestAuthenticator {
  if (config.authMode === authModeBearerToken && config.bearerToken !== undefined) {
    return new BearerTokenRequestAuthenticator(config.bearerToken);
  }
  if (config.authMode === authModeTrustedProxyHeader && config.trustedIdentityHeader !== undefined) {
    return new TrustedProxyHeaderRequestAuthenticator(config.trustedIdentityHeader);
  }
  throw new Error(`auth mode ${JSON.stringify(config.authMode)} is not configured`);
}

/** Thrown by a CallerPayloadCodecSelector to answer 403. */
export class CallerForbiddenError extends Error {
  public constructor() {
    super("the caller may not use payload keys");
  }
}

/**
 * Picks the codec, and so the keys, by authenticated caller.
 * Never pick keys from the request context: the browser controls it.
 */
export interface CallerPayloadCodecSelector {
  /**
   * @returns The codec this caller may use.
   * @throws {CallerForbiddenError} When the caller may not use any keys.
   */
  selectPayloadCodec(caller: CallerIdentity): Promise<PayloadCodec>;
}

/** Lets every authenticated caller use one codec. */
export class SingleTenantPayloadCodecSelector implements CallerPayloadCodecSelector {
  private readonly codec: PayloadCodec;

  public constructor(codec: PayloadCodec) {
    this.codec = codec;
  }

  public async selectPayloadCodec(_caller: CallerIdentity): Promise<PayloadCodec> {
    return this.codec;
  }
}

function sha256(text: string): Buffer {
  return createHash("sha256").update(text, "utf8").digest();
}
