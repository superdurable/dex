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
import type { AttributeLock } from "./persistence.js";
import type { StepMovement } from "./step.js";
import { requireName } from "./validation.js";

/**
 * Contains a typed RPC output and optional next-Step movements.
 * @typeParam Output - Value decoded for the Client caller.
 */
export interface RPCResult<Output> {
  /** Typed application result. */
  readonly output: Output;
  /** Step movements applied atomically with handler persistence writes. */
  readonly nextSteps?: readonly StepMovement<unknown>[];
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
  /** Required input codec for handlers that accept an input. */
  readonly inputCodec?: Codec<Input>;
  /** Required output codec for handlers returning RPCResult. */
  readonly outputCodec?: Codec<Output>;
  /** Non-negative handler timeout in milliseconds. */
  readonly timeoutMs?: number;
  /** Attribute locks held for the entire invocation. */
  readonly lockAttributes?: readonly AttributeLock[];
}

export interface RegisteredRPC {
  readonly method: Function;
  readonly name: string;
  readonly options: RPCOptions<any, any>;
}

const rpcOptions = new WeakMap<Function, RPCOptions<any, any>>();

/**
 * Decorates an RPC with both typed input and output.
 * @typeParam Input - Handler input type.
 * @typeParam Output - Handler output type.
 * @param options - Required input/output codecs and optional RPC settings.
 * @returns A stage-3 method decorator validated during Registry construction.
 */
export function rpc<Input, Output>(options: RPCOptions<Input, Output> & {
  /** Required codec for the handler input. */
  readonly inputCodec: Codec<Input>;
  /** Required codec for the RPCResult output. */
  readonly outputCodec: Codec<Output>;
}): <This>(
  method: (
    this: This,
    context: Context,
    input: Input,
  ) => RPCResult<Output> | Promise<RPCResult<Output>>,
  context: ClassMethodDecoratorContext<
    This,
    (
      this: This,
      context: Context,
      input: Input,
    ) => RPCResult<Output> | Promise<RPCResult<Output>>
  >,
) => void;

/**
 * Decorates an input-free RPC with typed output.
 * @typeParam Output - Handler output type.
 * @param options - Required output codec and optional RPC settings.
 * @returns A stage-3 method decorator validated during Registry construction.
 */
export function rpc<Output>(options: RPCOptions<never, Output> & {
  /** Input codecs are forbidden for an input-free handler. */
  readonly inputCodec?: never;
  /** Required codec for the RPCResult output. */
  readonly outputCodec: Codec<Output>;
}): <This>(
  method: (this: This, context: Context) => RPCResult<Output> | Promise<RPCResult<Output>>,
  context: ClassMethodDecoratorContext<
    This,
    (this: This, context: Context) => RPCResult<Output> | Promise<RPCResult<Output>>
  >,
) => void;

/**
 * Decorates a typed-input RPC with no output.
 * @typeParam Input - Handler input type.
 * @param options - Required input codec and optional RPC settings.
 * @returns A stage-3 method decorator validated during Registry construction.
 */
export function rpc<Input>(options: RPCOptions<Input, never> & {
  /** Required codec for the handler input. */
  readonly inputCodec: Codec<Input>;
  /** Output codecs are forbidden for a void handler. */
  readonly outputCodec?: never;
}): <This>(
  method: (this: This, context: Context, input: Input) => void | Promise<void>,
  context: ClassMethodDecoratorContext<
    This,
    (this: This, context: Context, input: Input) => void | Promise<void>
  >,
) => void;

/**
 * Decorates an input-free, output-free RPC.
 * @param options - Optional name, timeout, and Attribute locks.
 * @returns A stage-3 method decorator validated during Registry construction.
 */
export function rpc(options?: RPCOptions<never, never> & {
  /** Input codecs are forbidden for an input-free handler. */
  readonly inputCodec?: never;
  /** Output codecs are forbidden for a void handler. */
  readonly outputCodec?: never;
}): <This>(
  method: (this: This, context: Context) => void | Promise<void>,
  context: ClassMethodDecoratorContext<This, (this: This, context: Context) => void | Promise<void>>,
) => void;

/**
 * Creates a typed RPC method decorator.
 *
 * Registry construction validates handler shape, codecs, unique names, and locks.
 * The handler receives Context and optional input, then returns RPCResult or void.
 *
 * @example
 * ```ts
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
      });
    }
    prototype = Object.getPrototypeOf(prototype) as object | null;
  }
  return registered;
}
