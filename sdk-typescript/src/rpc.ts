// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Codec } from "./codec.js";
import type { Context } from "./context.js";
import type { Flow } from "./flow.js";
import type { AttributeLock, AttributeMapLoad } from "./persistence.js";
import type { ChannelLoad, ChannelMapLoad } from "./wait.js";
import type { StepClass, StepMovement } from "./step.js";
import { requireName } from "./validation.js";

/**
 * Contains a typed RPC output and optional next-Step movements.
 * @typeParam Output - Value decoded for the Client caller.
 */
export interface RPCResult<Output> {
  /** Typed application result. */
  readonly output: Output;
  /** Step movements applied atomically with handler persistence writes. */
  readonly nextSteps?: readonly StepMovement<any>[];
  /** Registered Step types canceled before `nextSteps` are scheduled. */
  readonly cancelingSteps?: readonly StepClass<any>[];
}

/**
 * Describes a typed, possibly asynchronous RPC handler.
 * @typeParam Input - Handler input type.
 * @typeParam Output - Handler output type.
 */
export type RPC<Input, Output> = (
  context: Context,
  input: Input,
) => RPCResult<Output> | Promise<RPCResult<Output>>;

/**
 * Configures an RPC decorator's name, codecs, timeout, and Attribute locks.
 * @typeParam Input - Handler input type.
 * @typeParam Output - Handler output type.
 */
export interface RPCOptions<Input = unknown, Output = unknown> {
  /** Protocol RPC name; uses the decorated method name when omitted. */
  readonly name?: string;
  /**
   * Input codec. Omit this for JSON objects, or when the handler has no input.
   * Scalar wire kinds still need an explicit codec.
   */
  readonly inputCodec?: Codec<Input>;
  /**
   * Output codec. Omit this for JSON objects, or when the handler returns void.
   * Scalar wire kinds still need an explicit codec.
   */
  readonly outputCodec?: Codec<Output>;
  /** Non-negative handler timeout in milliseconds. */
  readonly timeoutMs?: number;
  /** Attribute locks held for the entire invocation. */
  readonly lockAttributes?: readonly AttributeLock[];
  /** Requests transactional reads and writes even when no Attribute lock is configured. */
  readonly isTransactional?: boolean;
  /** AttributeMap instances included in the RPC snapshot. */
  readonly loadAttributeMaps?: readonly AttributeMapLoad[];
  /** Singleton Channel pending messages included in the RPC snapshot. */
  readonly loadChannels?: readonly ChannelLoad[];
  /** ChannelMap instance pending messages included in the RPC snapshot. */
  readonly loadChannelMaps?: readonly ChannelMapLoad[];
}

export interface RegisteredRPC {
  readonly method: Function;
  readonly name: string;
  readonly options: RPCOptions<any, any>;
  readonly hasInput: boolean;
}

const rpcOptions = new WeakMap<Function, RPCOptions<any, any>>();

/**
 * Decorates a Flow method as an RPC handler.
 * @typeParam Input - Handler input type.
 * @typeParam Output - Handler output type.
 * @param options - Optional codecs and RPC settings. Omitted codecs use JSON.
 * @returns A stage-3 method decorator validated during Registry construction.
 */
export function rpc<Input = unknown, Output = unknown>(
  options?: RPCOptions<Input, Output>,
): <This>(
  method: (this: This, context: Context, ...args: any[]) => unknown,
  context: ClassMethodDecoratorContext<This, (this: This, context: Context, ...args: any[]) => unknown>,
) => void;

/**
 * Creates a typed RPC method decorator.
 *
 * Registry construction validates handler shape, unique names, and locks.
 * The handler receives Context and optional input, then returns RPCResult or void.
 * Omitted input and output codecs use JSON. Scalar wire kinds still need an
 * explicit codec. A method with only a Context parameter and a void return is a
 * procedure. Default-parameter methods report `Function.length` without those
 * parameters, so pass `inputCodec` when a trailing input uses a default.
 *
 * @example
 * ```ts
 * @rpc()
 * describe(_context: Context, order: Order): RPCResult<Order> {
 *   return { output: order };
 * }
 *
 * @rpc({ inputCodec: stringCodec, outputCodec: booleanCodec, timeoutMs: 10_000 })
 * async cancel(context: Context, reason: string): Promise<RPCResult<boolean>> {
 *   return { output: true };
 * }
 * ```
 * @param options - Codecs and optional name, timeout, and lock settings.
 * @returns A stage-3 method decorator.
 * @throws {@link RangeError} when `timeoutMs` is negative.
 */
export function rpc(options: RPCOptions<any, any> = {}) {
  if (options.name !== undefined) {
    requireName(options.name);
  }
  if (options.timeoutMs !== undefined && options.timeoutMs < 0) {
    throw new RangeError("RPC timeout must be non-negative");
  }
  const immutableOptions = Object.freeze({ ...options });
  return function <This, Method extends (this: This, ...args: any[]) => unknown>(
    method: Method,
    _context: ClassMethodDecoratorContext<This, Method>,
  ): void {
    rpcOptions.set(method, immutableOptions);
  };
}

export function registeredRPCs(flow: Flow<unknown>): readonly RegisteredRPC[] {
  const registered: RegisteredRPC[] = [];
  const visitedNames = new Set<string>();
  let prototype: object | null = Object.getPrototypeOf(flow) as object | null;
  while (prototype !== null && prototype !== Object.prototype) {
    for (const name of Object.getOwnPropertyNames(prototype)) {
      if (name === "constructor" || visitedNames.has(name)) {
        continue;
      }
      visitedNames.add(name);
      const method = Object.getOwnPropertyDescriptor(prototype, name)?.value as unknown;
      if (typeof method !== "function") {
        continue;
      }
      const options = rpcOptions.get(method);
      if (options === undefined) {
        continue;
      }
      registered.push({
        method,
        name: options.name ?? name,
        options,
        hasInput: method.length >= 2 || options.inputCodec !== undefined,
      });
    }
    prototype = Object.getPrototypeOf(prototype) as object | null;
  }
  return registered;
}
