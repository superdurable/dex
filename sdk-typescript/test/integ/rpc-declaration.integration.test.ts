// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import assert from "node:assert/strict";
import test from "node:test";

import {
  Attribute,
  Channel,
  FlowDefinitionError,
  Registry,
  StepList,
  declareRPCMethod,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCOptions,
  type RPCResult,
} from "../../src/index.js";
import { registeredRPCs } from "../../src/rpc.js";
import { flowId, withEnvironment } from "./environment.js";
import { DeclaredRpcBaseFlow, DeclaredRpcFlow } from "./rpc_declaration_flow.js";

test("declared RPC methods are invoked through the Client alongside @rpc methods", async () => {
  const flow = new DeclaredRpcFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("rpc-declaration");
    await client.startFlow(flow, id, undefined);
    assert.equal(await client.invokeRPC(flow.setDeclaredValue, id, "first"), "first");
    assert.equal(await client.invokeRPC(flow.setDeclaredValue, id, "second"), "second");
    assert.equal(await client.invokeRPC(flow.getDeclaredValue, id), "second");
    assert.equal(await client.invokeRPC(flow.describeDeclaredRpcFlow, id), `${id}:second`);
    assert.equal(await client.invokeRPC(flow.getDeclaredWriteCount, id), 2);
    await client.stopFlow(id);
  });
});

test("declared RPC methods keep their options and explicit names", () => {
  const rpcs = registeredRPCs(new DeclaredRpcFlow() as Flow<unknown>);
  assert.deepEqual(
    rpcs.map((registered) => [registered.name, registered.hasInput]).sort(),
    [
      ["describeFlowWithDeclaredName", false],
      ["getDeclaredValue", false],
      ["getDeclaredWriteCount", false],
      ["setDeclaredValue", true],
    ],
  );
  const describe = rpcs.find((registered) => registered.name === "describeFlowWithDeclaredName");
  assert.equal(describe?.options.timeoutMs, 10_000);
  assert.ok(Object.isFrozen(describe?.options));
});

test("a method declared through a base class is an RPC of every subclass", async () => {
  class SiblingDeclaredRpcFlow extends DeclaredRpcBaseFlow {
    public getFlowType(): string {
      return "SiblingDeclaredRpcFlow";
    }
  }
  const sibling = new SiblingDeclaredRpcFlow();
  // setDeclaredValue was declared through DeclaredRpcFlow, but the method lives on the base class.
  assert.deepEqual(
    registeredRPCs(sibling as Flow<unknown>).map((registered) => registered.name).sort(),
    ["getDeclaredValue", "setDeclaredValue"],
  );
  await withEnvironment([sibling], async ({ client }) => {
    const id = flowId("rpc-declaration-sibling");
    await client.startFlow(sibling, id, undefined);
    await client.invokeRPC(sibling.setDeclaredValue, id, "inherited");
    assert.equal(await client.invokeRPC(sibling.getDeclaredValue, id), "inherited");
    await client.stopFlow(id);
  });
});

test("declaring a method twice or with @rpc is rejected", () => {
  class TwiceDeclaredFlow implements Flow {
    public getFlowType(): string {
      return "TwiceDeclaredFlow";
    }

    public getSteps() {
      return StepList.empty();
    }

    public getTwiceDeclaredValue(_context: Context): RPCResult<string> {
      return { output: "value" };
    }

    @rpc()
    public getDecoratedValue(_context: Context): RPCResult<string> {
      return { output: "value" };
    }
  }
  declareRPCMethod(TwiceDeclaredFlow, "getTwiceDeclaredValue");
  assert.throws(
    () => declareRPCMethod(TwiceDeclaredFlow, "getTwiceDeclaredValue", { name: "otherName" }),
    new TypeError(
      "RPC method TwiceDeclaredFlow.getTwiceDeclaredValue is already declared by @rpc or declareRPCMethod",
    ),
  );
  assert.throws(
    () => declareRPCMethod(TwiceDeclaredFlow, "getDecoratedValue"),
    new TypeError(
      "RPC method TwiceDeclaredFlow.getDecoratedValue is already declared by @rpc or declareRPCMethod",
    ),
  );
  // An inherited method that the base class already declared is the same function.
  class InheritedTwiceDeclaredFlow extends DeclaredRpcBaseFlow {
    public getFlowType(): string {
      return "InheritedTwiceDeclaredFlow";
    }
  }
  assert.throws(
    () => declareRPCMethod(InheritedTwiceDeclaredFlow, "getDeclaredValue"),
    new TypeError(
      "RPC method InheritedTwiceDeclaredFlow.getDeclaredValue is already declared by @rpc or declareRPCMethod",
    ),
  );
  assert.deepEqual(
    registeredRPCs(new TwiceDeclaredFlow() as Flow<unknown>).map((registered) => registered.name).sort(),
    ["getDecoratedValue", "getTwiceDeclaredValue"],
  );
});

test("declaring a member that is not a prototype method is rejected", () => {
  let accessorReads = 0;
  class InvalidMemberFlow implements Flow {
    public readonly arrowHandler = (_context: Context): RPCResult<string> => ({ output: "arrow" });
    public static readonly staticValue = 1;

    public getFlowType(): string {
      return "InvalidMemberFlow";
    }

    public getSteps() {
      return StepList.empty();
    }

    public get accessorHandler(): (context: Context) => RPCResult<string> {
      accessorReads += 1;
      return () => ({ output: "accessor" });
    }
  }
  Object.defineProperty(InvalidMemberFlow.prototype, "valueHandler", { value: "not a function" });
  const untyped = InvalidMemberFlow as unknown as Parameters<typeof declareRPCMethod>[0];
  const declareUntyped = (name: string) => declareRPCMethod(untyped, name as never);

  assert.throws(
    () => declareRPCMethod(InvalidMemberFlow, "arrowHandler"),
    new TypeError(
      "Flow class InvalidMemberFlow has no method arrowHandler; declare RPC handlers as class methods, not instance fields",
    ),
  );
  assert.throws(
    () => declareUntyped("missingHandler"),
    new TypeError(
      "Flow class InvalidMemberFlow has no method missingHandler; declare RPC handlers as class methods, not instance fields",
    ),
  );
  assert.throws(
    () => declareRPCMethod(InvalidMemberFlow, "accessorHandler"),
    new TypeError("Flow class InvalidMemberFlow member accessorHandler is an accessor, not an RPC method"),
  );
  assert.equal(accessorReads, 0);
  assert.throws(
    () => declareUntyped("valueHandler"),
    new TypeError("Flow class InvalidMemberFlow member valueHandler is not a method"),
  );
  assert.throws(
    () => declareUntyped("constructor"),
    new TypeError("Flow class InvalidMemberFlow constructor cannot be an RPC method"),
  );
  assert.throws(
    () => declareRPCMethod({} as unknown as typeof untyped, "getFlowType" as never),
    new TypeError("declareRPCMethod requires a Flow class"),
  );
});

const validatedValue = new Attribute("validated-value", stringCodec);
const unregisteredValue = new Attribute("unregistered-value", stringCodec);
const unregisteredChannel = new Channel("unregistered-channel", stringCodec);

interface ValidationCase {
  readonly description: string;
  readonly firstOptions: RPCOptions<any, any>;
  readonly secondOptions?: RPCOptions<any, any>;
}

const validationCases: readonly ValidationCase[] = [
  { description: "blank name", firstOptions: { name: "  " } },
  { description: "negative timeout", firstOptions: { timeoutMs: -1 } },
  {
    description: "duplicate RPC name",
    firstOptions: { name: "getValidatedValue" },
    secondOptions: { name: "getValidatedValue" },
  },
  { description: "unregistered Attribute lock", firstOptions: { lockAttributes: [unregisteredValue.lock()] } },
  { description: "unregistered Channel load", firstOptions: { loadChannels: [unregisteredChannel] } },
];

for (const validationCase of validationCases) {
  test(`declareRPCMethod reports the same ${validationCase.description} error as @rpc`, () => {
    const decorated = capturedFailure(() => decoratedValidationRegistry(validationCase));
    const declared = capturedFailure(() => declaredValidationRegistry(validationCase));
    assert.ok(decorated instanceof Error);
    assert.equal(declared.constructor, decorated.constructor);
    assert.equal(declared.message, decorated.message);
  });
}

test("Registry rejects a declared inherited method registered by two Flows", () => {
  class FirstInheritingFlow extends DeclaredRpcBaseFlow {
    public getFlowType(): string {
      return "FirstInheritingFlow";
    }
  }
  class SecondInheritingFlow extends DeclaredRpcBaseFlow {
    public getFlowType(): string {
      return "SecondInheritingFlow";
    }
  }
  assert.throws(
    () => new Registry([new FirstInheritingFlow(), new SecondInheritingFlow()]),
    FlowDefinitionError,
  );
});

function decoratedValidationRegistry(validationCase: ValidationCase): Registry {
  const firstOptions = validationCase.firstOptions;
  const secondOptions = validationCase.secondOptions ?? {};
  class ValidationFlow implements Flow {
    public getFlowType(): string {
      return "ValidationFlow";
    }

    public getSteps() {
      return StepList.empty();
    }

    public getPersistenceSchema(): PersistenceSchema {
      return { attributes: [validatedValue] };
    }

    @rpc(firstOptions)
    public getValidatedValue(context: Context): RPCResult<string> {
      return { output: validatedValue.get(context) };
    }

    @rpc(secondOptions)
    public describeValidatedValue(context: Context): RPCResult<string> {
      return { output: validatedValue.get(context) };
    }
  }
  return new Registry([new ValidationFlow()]);
}

function declaredValidationRegistry(validationCase: ValidationCase): Registry {
  class ValidationFlow implements Flow {
    public getFlowType(): string {
      return "ValidationFlow";
    }

    public getSteps() {
      return StepList.empty();
    }

    public getPersistenceSchema(): PersistenceSchema {
      return { attributes: [validatedValue] };
    }

    public getValidatedValue(context: Context): RPCResult<string> {
      return { output: validatedValue.get(context) };
    }

    public describeValidatedValue(context: Context): RPCResult<string> {
      return { output: validatedValue.get(context) };
    }
  }
  declareRPCMethod(ValidationFlow, "getValidatedValue", validationCase.firstOptions);
  declareRPCMethod(ValidationFlow, "describeValidatedValue", validationCase.secondOptions ?? {});
  return new Registry([new ValidationFlow()]);
}

function capturedFailure(operation: () => unknown): Error {
  try {
    operation();
  } catch (failure) {
    assert.ok(failure instanceof Error);
    return failure;
  }
  assert.fail("expected the RPC definition to be rejected");
}
