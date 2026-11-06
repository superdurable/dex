// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Codec } from "./codec.js";
import type { Context } from "./context.js";
import type { Flow } from "./flow.js";
import type { AttributeLock, AttributeMap, AttributeMapLoad } from "./persistence.js";
import type { Channel, ChannelMap, ChannelMapLoad } from "./wait.js";
import type { StepTarget, StepMovement } from "./step.js";
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
  /**
   * Registered Step types canceled before `nextSteps` are scheduled.
   *
   * Entries are classes, registered instances, or StepRef values; narrow them before use.
   * The Worker validates them when it applies this result. An invalid target fails the
   * RPC with InvalidStepResultError.
   */
  readonly cancelingSteps?: readonly StepTarget<any>[];
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
 * Configures an `@rpc` or `declareRPCMethod` declaration's name, codecs, timeout, and Attribute locks.
 * @typeParam Input - Handler input type.
 * @typeParam Output - Handler output type.
 */
export interface RPCOptions<Input = unknown, Output = unknown> {
  /** Protocol RPC name; uses the method name given to `@rpc` or `declareRPCMethod` when omitted. */
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
  /** AttributeMaps whose current instances are included in the RPC snapshot. */
  readonly loadAttributeMaps?: readonly AttributeMap<unknown>[];
  /** Exact AttributeMap instances included in the RPC snapshot. */
  readonly loadAttributeMapInstances?: readonly AttributeMapLoad[];
  /** Singleton Channel pending messages included in the RPC snapshot. */
  readonly loadChannels?: readonly Channel<unknown>[];
  /** ChannelMaps whose current instance messages are included in the RPC snapshot. */
  readonly loadChannelMaps?: readonly ChannelMap<unknown>[];
  /** Exact ChannelMap instance messages included in the RPC snapshot. */
  readonly loadChannelMapInstances?: readonly ChannelMapLoad[];
}

/**
 * Adds runtime-selected map instances to one RPC invocation.
 *
 * Dex unions these selections with the fixed locks and loads from `@rpc` or `declareRPCMethod`. Locking and loading
 * are independent, so read-modify-write handlers must select the same AttributeMap instance in
 * both fields.
 */
export interface RPCInvokeOptions {
  /** AttributeMap instance locks added for this invocation. */
  readonly lockAttributeMapInstances?: readonly AttributeLock[];
  /** Exact AttributeMap instance snapshots added for this invocation. */
  readonly loadAttributeMapInstances?: readonly AttributeMapLoad[];
  /** Exact ChannelMap pending-message snapshots added for this invocation. */
  readonly loadChannelMapInstances?: readonly ChannelMapLoad[];
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
 * Use {@link declareRPCMethod} instead when the runtime cannot transform decorators.
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
 * Use {@link declareRPCMethod} instead when the runtime cannot transform decorators.
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
  const immutableOptions = validatedImmutableRPCOptions(options);
  return function <This, Method extends (this: This, ...args: any[]) => unknown>(
    method: Method,
    _context: ClassMethodDecoratorContext<This, Method>,
  ): void {
    rpcOptions.set(method, immutableOptions);
  };
}

/**
 * Selects the public method names of a Flow type that can receive Context as their first argument.
 *
 * {@link declareRPCMethod} uses this type so its method name argument is checked at compile time.
 * A method qualifies when Context is assignable to its first parameter. `declareRPCMethod` also
 * requires at least one parameter; this rule is stricter than `@rpc`, which accepts methods with
 * no parameters. Private and protected methods are not included, so declare RPC methods as public.
 * The Flow timeout handler `handleTimeout` is excluded.
 *
 * The type also accepts inherited method names. `declareRPCMethod` rejects them at runtime; declare
 * an inherited method through the class that defines it.
 *
 * A call without these types, such as from JavaScript, is not checked at compile time. At runtime
 * `declareRPCMethod` rejects `handleTimeout` but no other Flow interface method, such as
 * `getFlowType`.
 * @typeParam FlowType - Flow instance type that declares the RPC method.
 * @since 1.35.0
 */
export type RPCMethodName<FlowType> = Exclude<
  {
    [Name in keyof FlowType]-?: FlowType[Name] extends (...args: infer Parameters) => unknown
      ? Parameters extends []
        ? never
        : Context extends Parameters[0]
          ? Name
          : never
      : never;
  }[keyof FlowType],
  "handleTimeout"
> & string;

/**
 * Declares a Flow class method as an RPC handler without decorator syntax.
 *
 * This function is the decorator-free form of {@link rpc}. Use it when the runtime that loads the
 * Flow module cannot transform decorators, such as Node.js type stripping with
 * `node --experimental-transform-types`. Call it once per RPC method at module top level, right
 * after the class declaration and before constructing the Registry. Pass the class itself and the
 * method name as a string literal. A Registry reads declarations when it is constructed, so a
 * declaration made after a Registry is built has no effect on that Registry.
 *
 * The declaration is stored exactly like `@rpc(options)` on the same method. Registry construction
 * then validates it with the same checks and the same errors: duplicate RPC names, locks on
 * unregistered Attributes, and invalid state loads. Client calls such as `invokeRPC(flow.describe,
 * ...)` work the same way for both forms.
 *
 * Declare a method through the class that defines it. The declaration belongs to the method
 * function, so a method declared on a base class is an RPC of every subclass that inherits it, as
 * `@rpc` on the base method would be. Naming a subclass for a method it only inherits throws,
 * because that would also change the base class and every sibling subclass. To give one subclass
 * its own RPC, override the method in that subclass and declare the override.
 *
 * The handler receives Context and optional input, then returns RPCResult or void. A method with
 * only a Context parameter and a void return is a procedure. Default-parameter methods report
 * `Function.length` without those parameters, so pass `inputCodec` when a trailing input uses a
 * default.
 *
 * A method can be declared once. This function throws when the method is already declared by an
 * earlier call or by `@rpc`. A Flow may mix both forms on different methods.
 *
 * Declare the handler as a class method. Instance fields, such as arrow functions, are not on the
 * class prototype and cannot be RPC handlers.
 *
 * @example
 * ```ts
 * import {
 *   Attribute,
 *   StepList,
 *   declareRPCMethod,
 *   gracefulComplete,
 *   stringCodec,
 *   type Context,
 *   type Flow,
 *   type PersistenceSchema,
 *   type RPCResult,
 *   type Step,
 *   type StepDecision,
 * } from "@superdurable/dex";
 *
 * const orderStatus = new Attribute("order-status", stringCodec);
 *
 * class RecordOrderStep implements Step<string> {
 *   public readonly inputCodec = stringCodec;
 *
 *   public getStepType(): string {
 *     return "RecordOrderStep";
 *   }
 *
 *   public execute(context: Context, status: string): StepDecision {
 *     orderStatus.set(context, status);
 *     return gracefulComplete(status);
 *   }
 * }
 *
 * export class OrderFlow implements Flow<string> {
 *   public getFlowType(): string {
 *     return "OrderFlow";
 *   }
 *
 *   public getSteps() {
 *     return StepList.startStep(new RecordOrderStep());
 *   }
 *
 *   public getPersistenceSchema(): PersistenceSchema {
 *     return { attributes: [orderStatus] };
 *   }
 *
 *   public getOrderStatus(context: Context): RPCResult<string> {
 *     return { output: orderStatus.get(context) };
 *   }
 *
 *   public updateOrderStatus(context: Context, status: string): RPCResult<string> {
 *     orderStatus.set(context, status);
 *     return { output: status };
 *   }
 * }
 *
 * declareRPCMethod(OrderFlow, "getOrderStatus", { outputCodec: stringCodec });
 * declareRPCMethod(OrderFlow, "updateOrderStatus", {
 *   inputCodec: stringCodec,
 *   outputCodec: stringCodec,
 *   lockAttributes: [orderStatus.lock()],
 * });
 *
 * // Later, on a Client whose Registry contains `orderFlow = new OrderFlow()`:
 * // const status = await client.invokeRPC(orderFlow.getOrderStatus, "order-42");
 * ```
 * @typeParam FlowType - Flow instance type constructed by `flowClass`.
 * @typeParam Input - Handler input type.
 * @typeParam Output - Handler output type.
 * @param flowClass - Flow class whose own prototype defines the method.
 * @param methodName - Name of a public method that takes Context first.
 * @param options - Optional codecs and RPC settings. Omitted codecs use JSON.
 * @throws {@link TypeError} when `flowClass` is not a class, the method is missing, is an
 * accessor, is not a function, is inherited rather than defined by `flowClass`, or is the
 * constructor or `handleTimeout`.
 * @throws {@link TypeError} when the method is already declared as an RPC handler.
 * @throws {@link TypeError} when `name` is blank.
 * @throws {@link RangeError} when `timeoutMs` is negative.
 * @since 1.35.0
 */
export function declareRPCMethod<FlowType extends object, Input = unknown, Output = unknown>(
  flowClass: abstract new (...args: never) => FlowType,
  methodName: RPCMethodName<FlowType>,
  options: RPCOptions<Input, Output> = {},
): void {
  const immutableOptions = validatedImmutableRPCOptions(options);
  const method = prototypeRPCMethod(flowClass, methodName);
  if (rpcOptions.has(method)) {
    throw new TypeError(
      `${rpcMethodLabel(flowClass, methodName)} is already declared by @rpc or declareRPCMethod`,
    );
  }
  rpcOptions.set(method, immutableOptions);
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

function validatedImmutableRPCOptions(options: RPCOptions<any, any>): RPCOptions<any, any> {
  if (options.name !== undefined) {
    requireName(options.name);
  }
  if (options.timeoutMs !== undefined && options.timeoutMs < 0) {
    throw new RangeError("RPC timeout must be non-negative");
  }
  return Object.freeze({ ...options });
}

function prototypeRPCMethod(flowClass: Function, methodName: string): Function {
  if (typeof flowClass !== "function" || typeof flowClass.prototype !== "object" || flowClass.prototype === null) {
    throw new TypeError("declareRPCMethod requires a Flow class");
  }
  if (methodName === "constructor") {
    throw new TypeError(`${flowClassLabel(flowClass)} constructor cannot be an RPC method`);
  }
  if (methodName === "handleTimeout") {
    throw new TypeError(`${flowClassLabel(flowClass)} handleTimeout is the Flow timeout handler, not an RPC method`);
  }
  // Read descriptors instead of the property so an accessor is never invoked.
  const descriptor = Object.getOwnPropertyDescriptor(flowClass.prototype, methodName);
  if (descriptor === undefined) {
    throw inheritedOrMissingMethodError(flowClass, methodName);
  }
  if (!("value" in descriptor)) {
    throw new TypeError(`${flowClassLabel(flowClass)} member ${methodName} is an accessor, not an RPC method`);
  }
  if (typeof descriptor.value !== "function") {
    throw new TypeError(`${flowClassLabel(flowClass)} member ${methodName} is not a method`);
  }
  return descriptor.value as Function;
}

function inheritedOrMissingMethodError(flowClass: Function, methodName: string): TypeError {
  let prototype = Object.getPrototypeOf(flowClass.prototype) as object | null;
  while (prototype !== null && prototype !== Object.prototype) {
    if (Object.getOwnPropertyDescriptor(prototype, methodName) !== undefined) {
      const definingClassName =
        (Object.getOwnPropertyDescriptor(prototype, "constructor")?.value as Function | undefined)?.name || "";
      const overrideHint = `override it in ${flowClass.name || "the subclass"} and declare the override`;
      if (definingClassName === "") {
        return new TypeError(
          `${flowClassLabel(flowClass)} method ${methodName} is inherited from an anonymous base class; ${overrideHint}`,
        );
      }
      return new TypeError(
        `${flowClassLabel(flowClass)} method ${methodName} is inherited from ${definingClassName}; ` +
          `declare it with declareRPCMethod(${definingClassName}, "${methodName}") or ${overrideHint}`,
      );
    }
    prototype = Object.getPrototypeOf(prototype) as object | null;
  }
  return new TypeError(
    `${flowClassLabel(flowClass)} has no method ${methodName}; declare RPC handlers as class methods, not instance fields`,
  );
}

function flowClassLabel(flowClass: Function): string {
  return flowClass.name ? `Flow class ${flowClass.name}` : "Anonymous Flow class";
}

function rpcMethodLabel(flowClass: Function, methodName: string): string {
  return flowClass.name
    ? `RPC method ${flowClass.name}.${methodName}`
    : `RPC method ${methodName} of an anonymous Flow class`;
}
