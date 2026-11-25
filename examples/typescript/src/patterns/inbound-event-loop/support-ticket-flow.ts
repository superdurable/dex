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
  AttributeMap,
  Channel,
  StepList,
  StepMovement,
  Timer,
  Wait,
  forceCompleteIfChannelsEmpty,
  goTo,
  jsonCodec,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

export const INACTIVITY_TIMEOUT_MS = 24 * 60 * 60 * 1_000;
export const MAX_REPLY_LENGTH = 4_000;
export const CURRENT_TRANSCRIPT_CHUNK = "current";
export const TRANSCRIPT_CHUNK_SIZE = 50;

export interface TicketReply {
  readonly sequence: number;
  readonly text: string;
}

export interface TranscriptChunk {
  readonly replies: readonly TicketReply[];
}

export const ticketReplies = new Channel("ticketReplies", stringCodec);
export const ticketResolutions = new Channel("ticketResolutions", stringCodec);
export const transcriptChunks = new AttributeMap(
  "transcriptChunks",
  jsonCodec<TranscriptChunk>(),
);

// dex:group group-id:inbound group-label:"Inbound messages"
// dex:explanation text:"Record each batch of replies, or route on the resolution or the inactivity Timer."
class AwaitTicketMessage implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "AwaitTicketMessage";
  }

  public getStepOptions(): StepOptions {
    return {
      executeLoadAttributeMapInstances: [transcriptChunks.load(CURRENT_TRANSCRIPT_CHUNK)],
    };
  }

  public waitFor(_context: Context, pendingResolution: string): Wait {
    if (pendingResolution !== "") {
      return Wait.until(ticketReplies.atLeast(1));
    }
    // Replies come before the resolution, so earlier replies are recorded first.
    return Wait.anyOf(
      ticketReplies.atLeast(1),
      ticketResolutions.forOne(),
      Timer.byDuration(INACTIVITY_TIMEOUT_MS),
    );
  }

  public execute(context: Context, pendingResolution: string): StepDecision {
    const replies = ticketReplies.results(context);
    if (replies.length > 0) {
      appendToTranscript(context, replies);
      if (pendingResolution !== "") {
        return goTo(CloseResolvedTicket, pendingResolution);
      }
      // A new execution registers a new wait, so the Timer restarts.
      return goTo(AwaitTicketMessage, "");
    }
    const resolutions = ticketResolutions.results(context);
    if (resolutions.length > 0) {
      return goTo(CloseResolvedTicket, resolutions[0]!);
    }
    if (context.hasTimerFired()) {
      return goTo(CloseInactiveTicket, undefined);
    }
    throw new Error("no Condition of the ticket wait completed");
  }
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Close the resolved ticket once no reply is left to record."
class CloseResolvedTicket implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "CloseResolvedTicket";
  }

  public execute(_context: Context, reason: string): StepDecision {
    return forceCompleteIfChannelsEmpty(
      `resolved: ${reason}`,
      StepMovement.of(AwaitTicketMessage, reason),
      ticketReplies,
    );
  }
}

// dex:group group-id:inactivity group-label:"Inactivity"
// dex:explanation text:"Close the quiet ticket once no reply or resolution is waiting."
class CloseInactiveTicket implements Step<void> {
  public getStepType(): string {
    return "CloseInactiveTicket";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return forceCompleteIfChannelsEmpty(
      "closed after inactivity",
      StepMovement.of(AwaitTicketMessage, ""),
      ticketReplies,
      ticketResolutions,
    );
  }
}

/** Records ticket replies until a resolution arrives or the ticket goes quiet. */
export class SupportTicketFlow implements Flow<string> {
  private readonly awaitTicketMessage = new AwaitTicketMessage();
  private readonly closeResolvedTicket = new CloseResolvedTicket();
  private readonly closeInactiveTicket = new CloseInactiveTicket();

  public getFlowType(): string {
    return "SupportTicketFlow";
  }

  public getSteps() {
    return StepList.startStep(this.awaitTicketMessage).otherSteps(
      this.closeResolvedTicket,
      this.closeInactiveTicket,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [transcriptChunks],
      channels: [ticketReplies, ticketResolutions],
    };
  }

  /** Validates one reply and queues it for the waiting Step. */
  @rpc({ inputCodec: stringCodec })
  public sendTicketReply(context: Context, text: string): void {
    if (text.trim() === "") {
      throw new Error("reply text is required");
    }
    if (text.length > MAX_REPLY_LENGTH) {
      throw new Error(`reply text exceeds ${MAX_REPLY_LENGTH} characters`);
    }
    ticketReplies.publish(context, text);
  }

  /** Queues the resolution; an empty reason would mean no resolution. */
  @rpc({ inputCodec: stringCodec })
  public resolveTicket(context: Context, reason: string): void {
    if (reason.trim() === "") {
      throw new Error("resolution reason is required");
    }
    ticketResolutions.publish(context, reason);
  }
}

/** Appends replies to the current chunk and archives each full chunk. */
function appendToTranscript(context: Context, texts: readonly string[]): void {
  let replies: TicketReply[] = [
    ...(transcriptChunks.get(context, CURRENT_TRANSCRIPT_CHUNK)?.replies ?? []),
  ];
  let nextSequence = replies.length > 0 ? replies[replies.length - 1]!.sequence + 1 : 1;
  for (const text of texts) {
    if (replies.length === TRANSCRIPT_CHUNK_SIZE) {
      transcriptChunks.set(context, archiveChunkToken(replies[0]!.sequence), { replies });
      replies = [];
    }
    replies.push({ sequence: nextSequence, text });
    nextSequence += 1;
  }
  transcriptChunks.set(context, CURRENT_TRANSCRIPT_CHUNK, { replies });
}

function archiveChunkToken(firstSequence: number): string {
  return String(firstSequence).padStart(20, "0");
}

export const supportTicketFlow = new SupportTicketFlow();
