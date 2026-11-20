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

import {
  AesGcmPayloadCodec,
  Attribute,
  Client,
  FilePayloadEncryptionKeyProvider,
  Registry,
  StepList,
  Worker,
  gracefulComplete,
  rpc,
  stringCodec,
  type BlobCache,
  type Context,
  type Flow,
  type PayloadCodec,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

/** The customer's text, so payload encryption stores it as ciphertext. */
export const ticketMessage = new Attribute("support-ticket-message", stringCodec);
/** Stays plaintext, so waitForAttributeMatch and Action conditions can compare it. */
export const ticketStatus = new Attribute("support-ticket-status", stringCodec).plaintext();

class ReceiveTicketStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "ReceiveTicketStep";
  }

  public execute(context: Context, message: string): StepDecision {
    ticketMessage.set(context, message);
    ticketStatus.set(context, "received");
    return gracefulComplete("received");
  }
}

export class SupportTicketFlow implements Flow<string> {
  private readonly receiveTicketStep = new ReceiveTicketStep();

  public getFlowType(): string {
    return "SupportTicketFlow";
  }

  public getSteps() {
    return StepList.startStep(this.receiveTicketStep);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [ticketMessage, ticketStatus] };
  }

  @rpc({ outputCodec: stringCodec })
  public getTicketMessage(context: Context): RPCResult<string> {
    return { output: ticketMessage.get(context) };
  }
}

/** Reads a key file, such as one from dexcli codec-server keygen. */
export async function newFilePayloadCodec(keyFilePath: string): Promise<PayloadCodec> {
  const keyProvider = await FilePayloadEncryptionKeyProvider.fromFile(keyFilePath);
  return new AesGcmPayloadCodec(keyProvider);
}

export async function startEncryptedSupportTicket(
  cache: BlobCache,
  serverAddress: string,
  keyFilePath: string,
): Promise<{ flow: SupportTicketFlow; worker: Worker; client: Client }> {
  const payloadCodec = await newFilePayloadCodec(keyFilePath);
  const flow = new SupportTicketFlow();
  const registry = new Registry([flow]);
  const worker = new Worker(registry, cache, {
    serverAddress,
    pull: { maxConcurrentTasks: 8 },
    payloadCodec,
  });
  try {
    await worker.start();
    await worker.waitPullRegistered({ timeoutMs: 30_000 });
  } catch (failure) {
    try {
      await worker.close();
    } catch (cleanupFailure) {
      throw new AggregateError([failure, cleanupFailure], "pull Worker startup and cleanup failed");
    }
    throw failure;
  }
  const client = new Client(registry, cache, { serverAddress, payloadCodec });
  return { flow, worker, client };
}

export async function stopEncryptedSupportTicket(worker: Worker, client: Client): Promise<void> {
  await client.close();
  await worker.close();
}
