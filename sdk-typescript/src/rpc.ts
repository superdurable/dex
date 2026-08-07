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

export interface RPCResult<Output> {
  readonly output: Output;
  readonly nextSteps?: readonly StepMovement<unknown>[];
}

export type RPC<Input, Output> = (context: Context, input: Input) => RPCResult<Output>;

export interface RPCOptions<Input = unknown, Output = unknown> {
  readonly name?: string;
  readonly inputCodec?: Codec<Input>;
  readonly outputCodec?: Codec<Output>;
  readonly timeoutMs?: number;
  readonly lockAttributes?: readonly AttributeLock[];
}

export interface RegisteredRPC {
  readonly method: Function;
  readonly name: string;
  readonly options: RPCOptions<any, any>;
}

const rpcOptions = new WeakMap<Function, RPCOptions<any, any>>();

export function rpc<Input, Output>(options: RPCOptions<Input, Output> & {
  readonly inputCodec: Codec<Input>;
  readonly outputCodec: Codec<Output>;
}): <This>(
  method: (this: This, context: Context, input: Input) => RPCResult<Output>,
  context: ClassMethodDecoratorContext<
    This,
    (this: This, context: Context, input: Input) => RPCResult<Output>
  >,
) => void;

export function rpc<Output>(options: RPCOptions<never, Output> & {
  readonly inputCodec?: never;
  readonly outputCodec: Codec<Output>;
}): <This>(
  method: (this: This, context: Context) => RPCResult<Output>,
  context: ClassMethodDecoratorContext<
    This,
    (this: This, context: Context) => RPCResult<Output>
  >,
) => void;

export function rpc<Input>(options: RPCOptions<Input, never> & {
  readonly inputCodec: Codec<Input>;
  readonly outputCodec?: never;
}): <This>(
  method: (this: This, context: Context, input: Input) => void,
  context: ClassMethodDecoratorContext<This, (this: This, context: Context, input: Input) => void>,
) => void;

export function rpc(options?: RPCOptions<never, never> & {
  readonly inputCodec?: never;
  readonly outputCodec?: never;
}): <This>(
  method: (this: This, context: Context) => void,
  context: ClassMethodDecoratorContext<This, (this: This, context: Context) => void>,
) => void;

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
