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
  Channel,
  StepList,
  Timer,
  Wait,
  doubleCodec,
  goTo,
  gracefulComplete,
  jsonCodec,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

const approvalMessages = new Channel("ApprovalMessages", stringCodec);
export const queuedMessages = new Channel("QueuedMessages", stringCodec);
export const prioritizedMessages = new Channel("PrioritizedMessages", stringCodec);

export interface QueuedMessageReference {
  readonly messageId: string;
}

export interface PendingMessage {
  readonly messageId: string;
  readonly value: string;
}

const queuedMessageReferenceCodec = jsonCodec<QueuedMessageReference>();
const pendingMessagesCodec = jsonCodec<readonly PendingMessage[]>();

class ChannelWait implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "ChannelWait";
  }

  public getStepOptions(): StepOptions {
    return { executeLoadChannels: [queuedMessages] };
  }

  public waitFor(_context: Context, input: number): Wait {
    return Wait.anyOf(
      approvalMessages.forOne(),
      Timer.byDuration(input * 1000),
    );
  }

  public execute(context: Context, _input: number): StepDecision {
    const pendingQueuedMessages = queuedMessages.pendingMessages(context);
    if (pendingQueuedMessages.length > 0) {
      queuedMessages.delete(context, pendingQueuedMessages[0]!.messageId);
      return gracefulComplete(pendingQueuedMessages[0]!.value);
    }
    if (context.hasTimerFired()) {
      return gracefulComplete("approval timed out");
    }
    const approvalMessageValues = approvalMessages.results(context);
    return gracefulComplete(approvalMessageValues[0]!);
  }
}

const channelWaitStep = new ChannelWait();

export class ChannelFlow implements Flow<number> {
  public getFlowType(): string {
    return "ChannelFlow";
  }

  public getSteps() {
    return StepList.startStep(channelWaitStep);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [approvalMessages, queuedMessages, prioritizedMessages] };
  }

  @rpc()
  public publishApprovalMessage(context: Context): void {
    approvalMessages.publish(context, "approved");
  }

  @rpc({ inputCodec: stringCodec })
  public enqueueChannelMessage(context: Context, value: string): void {
    queuedMessages.publish(context, value);
  }

  @rpc({ loadChannels: [queuedMessages], outputCodec: pendingMessagesCodec })
  public getQueuedMessages(context: Context): RPCResult<readonly PendingMessage[]> {
    return { output: queuedMessages.pendingMessages(context) };
  }

  @rpc({
    isTransactional: true,
    loadChannels: [queuedMessages],
    inputCodec: queuedMessageReferenceCodec,
  })
  public deleteQueuedMessage(context: Context, queuedMessage: QueuedMessageReference): void {
    queuedMessages.delete(context, queuedMessage.messageId);
  }

  @rpc({ loadChannels: [prioritizedMessages], outputCodec: pendingMessagesCodec })
  public getPrioritizedMessages(context: Context): RPCResult<readonly PendingMessage[]> {
    return { output: prioritizedMessages.pendingMessages(context) };
  }

  @rpc({
    isTransactional: true,
    loadChannels: [queuedMessages],
    inputCodec: queuedMessageReferenceCodec,
  })
  public moveQueuedMessageToPrioritizedMessages(
    context: Context,
    queuedMessage: QueuedMessageReference,
  ): void {
    const messageToPrioritize = queuedMessages.findPendingMessage(
      context,
      queuedMessage.messageId,
    );
    queuedMessages.delete(context, queuedMessage.messageId);
    if (messageToPrioritize !== undefined) {
      prioritizedMessages.publish(context, messageToPrioritize.value);
    }
  }
}

export const channelFlow = new ChannelFlow();
