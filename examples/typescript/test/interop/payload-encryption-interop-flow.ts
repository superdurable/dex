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

// The Flow that the cross-SDK payload encryption interoperability test runs.
// Every SDK defines the same Flow shape under its own Flow type. Each SDK's
// Worker serves only its own Flow type, and every SDK's Client starts and reads
// all three. Keep this file in sync with examples/go/interop/payloadencryption/
// and examples/python/tests/interop/.

import {
  AesGcmPayloadCodec,
  Attribute,
  FilePayloadEncryptionKeyProvider,
  Registry,
  StepList,
  doubleCodec,
  gracefulComplete,
  int64Codec,
  jsonCodec,
  stringCodec,
  type Context,
  type Flow,
  type PayloadEncryptionOptions,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

/** One SDK in the interop test. */
export type PayloadInteropOwner = "go" | "python" | "typescript";

/** Every SDK whose Worker takes part in the interop test. */
export const payloadInteropOwners: readonly PayloadInteropOwner[] = ["go", "python", "typescript"];

/** Makes the Step fail with an error that names the rest of the input. */
export const failInputPrefix = "fail:";

/** A JSON object with nested fields, arrays, and non-ASCII text. */
export interface PayloadInteropDocument {
  readonly title: string;
  readonly tags: readonly string[];
  readonly nested: {
    readonly level: number;
    readonly weight: number;
    readonly flags: readonly boolean[];
    readonly note: string;
  };
}

export interface PayloadInteropInput {
  readonly text: string;
  readonly count: number;
  readonly ratio: number;
  readonly document: PayloadInteropDocument;
}

export const secretText = new Attribute("payload-interop-secret", stringCodec);
export const statusText = new Attribute("payload-interop-status", stringCodec).plaintext();
export const documentObject = new Attribute("payload-interop-document", jsonCodec<PayloadInteropDocument>());
export const countInteger = new Attribute("payload-interop-count", int64Codec);
export const ratioDouble = new Attribute("payload-interop-ratio", doubleCodec);

/** The int64 every Client sends; it needs more than 32 bits. */
export const expectedCount = -1234567890123n;
export const expectedRatio = 3.141592653589793;
export const expectedDocument: PayloadInteropDocument = {
  title: "naïve café ✓ Grüße €",
  tags: ["alpha", "beta ✓", ""],
  nested: { level: 3, weight: 1.25, flags: [true, false], note: "ü ß ✓" },
};

export function newPayloadInteropInput(text: string): PayloadInteropInput {
  return { text, count: Number(expectedCount), ratio: expectedRatio, document: expectedDocument };
}

export function expectedSecretText(owner: PayloadInteropOwner, input: string): string {
  return `${input} | stored by ${owner}`;
}

export function expectedStatusText(owner: PayloadInteropOwner): string {
  return `stored by ${owner}`;
}

export function expectedOutput(owner: PayloadInteropOwner, input: string): string {
  return `completed by ${owner}: ${input}`;
}

/** Returns the Step error text for the input after failInputPrefix. */
export function expectedFailureText(owner: PayloadInteropOwner, input: string): string {
  return `payload interop failure in ${owner}: ${input}`;
}

class StorePayloadInteropSecretStep implements Step<PayloadInteropInput> {
  public readonly inputCodec = jsonCodec<PayloadInteropInput>();

  public constructor(private readonly owner: PayloadInteropOwner) {}

  public getStepType(): string {
    return "StorePayloadInteropSecretStep";
  }

  // One attempt, so a failing Step fails the Flow at once.
  public getStepOptions(): StepOptions {
    return { executeRetry: { maximumAttempts: 1 } };
  }

  public execute(context: Context, input: PayloadInteropInput): StepDecision {
    if (input.text.startsWith(failInputPrefix)) {
      throw new Error(expectedFailureText(this.owner, input.text.slice(failInputPrefix.length)));
    }
    secretText.set(context, expectedSecretText(this.owner, input.text));
    statusText.set(context, expectedStatusText(this.owner));
    documentObject.set(context, input.document);
    countInteger.set(context, BigInt(input.count));
    ratioDouble.set(context, input.ratio);
    return gracefulComplete(expectedOutput(this.owner, input.text));
  }
}

abstract class PayloadInteropFlow implements Flow<PayloadInteropInput> {
  private readonly storePayloadInteropSecretStep: StorePayloadInteropSecretStep;

  protected constructor(owner: PayloadInteropOwner) {
    this.storePayloadInteropSecretStep = new StorePayloadInteropSecretStep(owner);
  }

  public abstract getFlowType(): string;

  public getSteps() {
    return StepList.startStep(this.storePayloadInteropSecretStep);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [secretText, statusText, documentObject, countInteger, ratioDouble] };
  }
}

/** The TypeScript Client's definition of the Flow the Go Worker serves. */
export class GoPayloadInteropFlow extends PayloadInteropFlow {
  public constructor() {
    super("go");
  }

  public getFlowType(): string {
    return "GoPayloadInteropFlow";
  }
}

/** The TypeScript Client's definition of the Flow the Python Worker serves. */
export class PythonPayloadInteropFlow extends PayloadInteropFlow {
  public constructor() {
    super("python");
  }

  public getFlowType(): string {
    return "PythonPayloadInteropFlow";
  }
}

/** The interop Flow that the TypeScript Worker serves. */
export class TypeScriptPayloadInteropFlow extends PayloadInteropFlow {
  public constructor() {
    super("typescript");
  }

  public getFlowType(): string {
    return "TypeScriptPayloadInteropFlow";
  }
}

/** Returns every Owner's Flow, so one Client can start them all. */
export function newClientFlows(): Map<PayloadInteropOwner, PayloadInteropFlow> {
  return new Map<PayloadInteropOwner, PayloadInteropFlow>([
    ["go", new GoPayloadInteropFlow()],
    ["python", new PythonPayloadInteropFlow()],
    ["typescript", new TypeScriptPayloadInteropFlow()],
  ]);
}

export function newClientRegistry(flows: Map<PayloadInteropOwner, PayloadInteropFlow>): Registry {
  return new Registry([...flows.values()]);
}

/** Reads a key file, such as one from dexcli codec-server keygen, and encrypts every Value kind. */
export async function newFilePayloadEncryption(keyFilePath: string): Promise<PayloadEncryptionOptions> {
  const payloadCodec = new AesGcmPayloadCodec(await FilePayloadEncryptionKeyProvider.fromFile(keyFilePath));
  return { payloadCodec, encryptStringValues: true, encryptPrimitiveValues: true };
}

export function requiredEnv(name: string): string {
  const value = process.env[name];
  if (value === undefined || value === "") {
    throw new Error(`${name} is required`);
  }
  return value;
}
