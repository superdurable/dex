// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { idempotencyKeyTimeMs } from "./request-keys.js";
import { RequestOutcomeTable, type LocalRequestOutcome } from "./request-outcome-table.js";
import type { ChannelMessage } from "./terminal-ui-channel.js";
import { terminalUiChannelValidator } from "./terminal-ui-schemas.js";

/** The most requests, other than `get_request_result`, that may wait for a response at once. */
export const maxInFlightLocalRequests = 4;
const maxRequestId = Number.MAX_SAFE_INTEGER;
const methodNamePattern = /^[a-z][a-z0-9_]*$/;

/** Runs one local request method. A rejection with {@link WorkerHostStoppedError} answers `host_stopped`. */
export type LocalRequestMethod = (
  params: Readonly<Record<string, unknown>>,
  idempotencyKey: string,
) => Promise<LocalRequestOutcome>;

/** The Worker host is gone, so a relayed request cannot finish. */
export class WorkerHostStoppedError extends Error {}

/** What the relay did with one message from the terminal UI after `ready`. */
export type TerminalUiMessageHandling = "request" | "cancel" | "skipped" | "malformed";

/** One `request` message from the terminal UI, after its framing passed. */
export interface LocalRequestMessage {
  readonly id: number;
  readonly method: string;
  readonly idempotencyKey: unknown;
  readonly params: unknown;
}

/**
 * The launcher side of terminal UI contract 1's local requests: checks each request, answers `busy`,
 * `unknown_method`, and `invalid_params`, remembers each key's outcome, serves `get_request_result`, and answers
 * `cancel`. Every request gets exactly one response.
 */
export class LocalRequestRelay {
  private readonly table: RequestOutcomeTable;
  private readonly awaitingResponse = new Map<number, string>();
  private inFlightCount = 0;

  /**
   * Creates the relay.
   * @param methods - The methods the launch document lists, except `get_request_result`, with their handlers.
   * @param isRequestResultServed - Whether the launch document lists `get_request_result`.
   * @param sendResponse - Writes one `response` message.
   * @param now - The clock, in milliseconds.
   */
  public constructor(
    private readonly methods: ReadonlyMap<string, LocalRequestMethod>,
    private readonly isRequestResultServed: boolean,
    private readonly sendResponse: (id: number, outcome: LocalRequestOutcome) => void,
    now: () => number = Date.now,
  ) {
    this.table = new RequestOutcomeTable(now);
  }

  /**
   * Returns whether a request's `id` can pair a response: an integer from 1 to 2^53−1.
   * @param id - The `id` field.
   */
  public static isRequestId(id: unknown): id is number {
    return typeof id === "number" && Number.isInteger(id) && id >= 1 && id <= maxRequestId;
  }

  /**
   * Handles one message the terminal UI sent after `ready`. A message that fails the channel schema's terminal UI
   * side is malformed: a request with a usable `id` gets `invalid_params`, and anything else is dropped. A
   * well-formed type that contract 1 does not define is skipped.
   * @param message - The message.
   * @returns What it did.
   */
  public handleMessage(message: ChannelMessage): TerminalUiMessageHandling {
    const isValid: boolean = terminalUiChannelValidator("from_tui")(message as unknown);
    if (!isValid) {
      if (message.type === "request" && LocalRequestRelay.isRequestId(message.id)) {
        this.sendResponse(message.id, refusal("invalid_params", "The request does not match terminal UI contract 1."));
      }
      return "malformed";
    }
    if (message.type === "request") {
      this.handleRequest({
        id: message.id as number,
        method: message.method as string,
        idempotencyKey: message.idempotency_key,
        params: message.params,
      });
      return "request";
    }
    if (message.type === "cancel") {
      this.handleCancel(message.id as number);
      return "cancel";
    }
    return "skipped";
  }

  /**
   * Handles one request. It answers synchronously when it can, so a `cancel` that follows it on the channel finds
   * it waiting.
   * @param request - The request.
   */
  public handleRequest(request: LocalRequestMessage): void {
    if (request.method === "get_request_result") {
      this.answerRequestResult(request);
      return;
    }
    const handler = this.methods.get(request.method);
    if (handler === undefined) {
      this.sendResponse(request.id, refusal("unknown_method", `This command does not serve ${describeMethod(request.method)}.`));
      return;
    }
    if (typeof request.idempotencyKey !== "string" || idempotencyKeyTimeMs(request.idempotencyKey) === undefined) {
      this.sendResponse(request.id, refusal("invalid_params", "The idempotency key is not a lowercase UUIDv7."));
      return;
    }
    if (!isPlainObject(request.params)) {
      this.sendResponse(request.id, refusal("invalid_params", "params must be an object."));
      return;
    }
    if (this.inFlightCount >= maxInFlightLocalRequests) {
      this.sendResponse(request.id, refusal("busy", `${maxInFlightLocalRequests} local requests are already running.`));
      return;
    }
    const key = request.idempotencyKey;
    const paramsText = canonicalJson(request.params);
    let entry = this.table.find(key);
    if (entry !== undefined && (entry.method !== request.method || entry.paramsText !== paramsText)) {
      this.sendResponse(request.id, refusal("invalid_params", "This idempotency key came with another request."));
      return;
    }
    if (entry === undefined) {
      const params = request.params;
      const outcome = this.runMethod(handler, params, key);
      entry = { method: request.method, paramsText, outcome };
      this.table.remember(key, entry);
    }
    this.awaitResponse(request.id, request.method, entry.outcome);
  }

  /**
   * Handles one `cancel`: answers `cancelled` when the request still waits, and otherwise does nothing.
   * @param id - The cancelled request's `id`.
   */
  public handleCancel(id: number): void {
    const method = this.awaitingResponse.get(id);
    if (method !== undefined) {
      this.awaitingResponse.delete(id);
      this.inFlightCount -= 1;
      this.sendResponse(id, refusal("cancelled", `Stopped waiting for ${method}.`));
    }
  }

  private async runMethod(
    handler: LocalRequestMethod,
    params: Readonly<Record<string, unknown>>,
    key: string,
  ): Promise<LocalRequestOutcome> {
    try {
      return await handler(params, key);
    } catch (error: unknown) {
      // The operation's real outcome is not known, so get_request_result must not report one.
      this.table.forget(key);
      return error instanceof WorkerHostStoppedError
        ? refusal("host_stopped", "The Worker host stopped.")
        : refusal("failed", "The request failed in the command.");
    }
  }

  private awaitResponse(id: number, method: string, outcome: Promise<LocalRequestOutcome>): void {
    this.awaitingResponse.set(id, method);
    this.inFlightCount += 1;
    void outcome.then((settled) => {
      if (this.awaitingResponse.delete(id)) {
        this.inFlightCount -= 1;
        this.sendResponse(id, settled);
      }
    });
  }

  private answerRequestResult(request: LocalRequestMessage): void {
    if (!this.isRequestResultServed) {
      this.sendResponse(request.id, refusal("unknown_method", "This command does not serve get_request_result."));
      return;
    }
    const params = request.params;
    const key = isPlainObject(params) ? params.idempotency_key : undefined;
    if (request.idempotencyKey !== undefined || typeof key !== "string" || idempotencyKeyTimeMs(key) === undefined) {
      this.sendResponse(request.id, refusal("invalid_params", "get_request_result needs params.idempotency_key, a lowercase UUIDv7."));
      return;
    }
    const entry = this.table.find(key);
    if (entry?.finished === undefined) {
      this.sendResponse(request.id, { result: { state: entry === undefined ? "unknown" : "running" } });
      return;
    }
    this.sendResponse(request.id, { result: { state: "done", ...entry.finished.outcome } });
  }
}

/**
 * Returns an error outcome.
 * @param code - The lowercase snake_case code.
 * @param message - One English sentence without secrets or environment values.
 */
export function refusal(code: string, message: string): LocalRequestOutcome {
  return { error: { code, message } };
}

function describeMethod(method: string): string {
  return methodNamePattern.test(method) ? method : "that method";
}

function isPlainObject(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** JSON with object keys sorted, so two equal `params` compare equal. */
function canonicalJson(value: unknown): string {
  if (Array.isArray(value)) {
    return `[${value.map((item) => canonicalJson(item)).join(",")}]`;
  }
  if (isPlainObject(value)) {
    const keys = Object.keys(value).sort();
    return `{${keys.map((key) => `${JSON.stringify(key)}:${canonicalJson(value[key])}`).join(",")}}`;
  }
  return JSON.stringify(value) ?? "null";
}
