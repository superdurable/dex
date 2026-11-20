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
// all three. Keep this file in sync with
// examples/go/interop/payloadencryption/payload_interop_flow.go and
// examples/python/tests/interop/payload_encryption_interop_flow.py.

import {
  AesGcmPayloadCodec,
  Attribute,
  FilePayloadEncryptionKeyProvider,
  Registry,
  StepList,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type PayloadCodec,
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

export const secretText = new Attribute("payload-interop-secret", stringCodec);
export const statusText = new Attribute("payload-interop-status", stringCodec).plaintext();

const flowTypes: Record<PayloadInteropOwner, string> = {
  go: "GoPayloadInteropFlow",
  python: "PythonPayloadInteropFlow",
  typescript: "TypeScriptPayloadInteropFlow",
};

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

class StorePayloadInteropSecretStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly owner: PayloadInteropOwner) {}

  public getStepType(): string {
    return "StorePayloadInteropSecretStep";
  }

  // One attempt, so a failing Step fails the Flow at once.
  public getStepOptions(): StepOptions {
    return { executeRetry: { maximumAttempts: 1 } };
  }

  public execute(context: Context, input: string): StepDecision {
    if (input.startsWith(failInputPrefix)) {
      throw new Error(expectedFailureText(this.owner, input.slice(failInputPrefix.length)));
    }
    secretText.set(context, expectedSecretText(this.owner, input));
    statusText.set(context, expectedStatusText(this.owner));
    return gracefulComplete(expectedOutput(this.owner, input));
  }
}

export class PayloadInteropFlow implements Flow<string> {
  private readonly storePayloadInteropSecretStep: StorePayloadInteropSecretStep;

  public constructor(public readonly owner: PayloadInteropOwner) {
    this.storePayloadInteropSecretStep = new StorePayloadInteropSecretStep(owner);
  }

  public getFlowType(): string {
    return flowTypes[this.owner];
  }

  public getSteps() {
    return StepList.startStep(this.storePayloadInteropSecretStep);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [secretText, statusText] };
  }
}

/** Returns every Owner's Flow, so one Client can start them all. */
export function newClientFlows(): Map<PayloadInteropOwner, PayloadInteropFlow> {
  return new Map(payloadInteropOwners.map((owner) => [owner, new PayloadInteropFlow(owner)]));
}

export function newClientRegistry(flows: Map<PayloadInteropOwner, PayloadInteropFlow>): Registry {
  return new Registry([...flows.values()]);
}

/** Reads a key file, such as one from dexcli codec-server keygen. */
export async function newFilePayloadCodec(keyFilePath: string): Promise<PayloadCodec> {
  return new AesGcmPayloadCodec(await FilePayloadEncryptionKeyProvider.fromFile(keyFilePath));
}

export function requiredEnv(name: string): string {
  const value = process.env[name];
  if (value === undefined || value === "") {
    throw new Error(`${name} is required`);
  }
  return value;
}
