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

package io.superdurable.dex.patterns.inboundeventloop;

import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.util.ArrayList;
import java.util.List;

/** Records ticket replies until a resolution arrives or the ticket goes quiet. */
@Component
public class SupportTicketConversationFlow implements Flow<String> {
    public static final Duration INACTIVITY_TIMEOUT = Duration.ofHours(24);
    public static final int MAX_REPLY_LENGTH = 4000;
    public static final String CURRENT_TRANSCRIPT_CHUNK = "current";
    public static final int TRANSCRIPT_CHUNK_SIZE = 50;

    public static final Channel<String> ticketReplies =
            Channel.define("ticketReplies", String.class);
    public static final Channel<String> ticketResolutions =
            Channel.define("ticketResolutions", String.class);
    public static final AttributeMap<TranscriptChunk> transcriptChunks =
            AttributeMap.define("transcriptChunks", TranscriptChunk.class);

    private final AwaitTicketMessage awaitTicketMessage = new AwaitTicketMessage();
    private final CloseResolvedTicket closeResolvedTicket = new CloseResolvedTicket();
    private final CloseInactiveTicket closeInactiveTicket = new CloseInactiveTicket();

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(awaitTicketMessage)
                .otherSteps(closeResolvedTicket, closeInactiveTicket);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(ticketReplies, ticketResolutions, transcriptChunks);
    }

    /** Validates one reply and queues it for the waiting Step. */
    @RPC
    public void sendTicketReply(final Context context, final String text) {
        if (text == null || text.isBlank()) {
            throw new IllegalArgumentException("reply text is required");
        }
        if (text.codePointCount(0, text.length()) > MAX_REPLY_LENGTH) {
            throw new IllegalArgumentException(
                    "reply text exceeds " + MAX_REPLY_LENGTH + " characters");
        }
        ticketReplies.publish(context, text);
    }

    /** Queues the resolution; an empty reason would mean no resolution. */
    @RPC
    public void resolveTicket(final Context context, final String reason) {
        if (reason == null || reason.isBlank()) {
            throw new IllegalArgumentException("resolution reason is required");
        }
        ticketResolutions.publish(context, reason);
    }

    // dex:group group-id:inbound group-label:"Inbound messages"
    // dex:explanation text:"Record each batch of replies, or route on the resolution or the inactivity Timer."
    /** Waits for replies, a resolution, or inactivity; the input is a pending resolution. */
    static final class AwaitTicketMessage implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String pendingResolution) {
            if (!pendingResolution.isEmpty()) {
                return Wait.until(ticketReplies.atLeast(1));
            }
            // Replies come before the resolution, so earlier replies are recorded first.
            return Wait.anyOf(
                    ticketReplies.atLeast(1),
                    ticketResolutions.forOne(),
                    Timer.byDuration(INACTIVITY_TIMEOUT));
        }

        @Override
        public StepDecision execute(final Context context, final String pendingResolution) {
            final List<String> replies = ticketReplies.getConditionResults(context);
            if (!replies.isEmpty()) {
                appendToTranscript(context, replies);
                if (!pendingResolution.isEmpty()) {
                    return StepDecision.goTo(CloseResolvedTicket.class, pendingResolution);
                }
                // A new execution registers a new wait, so the Timer restarts.
                return StepDecision.goTo(AwaitTicketMessage.class, "");
            }
            final List<String> resolutions = ticketResolutions.getConditionResults(context);
            if (!resolutions.isEmpty()) {
                return StepDecision.goTo(CloseResolvedTicket.class, resolutions.get(0));
            }
            if (context.hasTimerFired()) {
                return StepDecision.goTo(CloseInactiveTicket.class, null);
            }
            throw new IllegalStateException("no Condition of the ticket wait completed");
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .addExecuteLoadAttributeMapInstance(transcriptChunks, CURRENT_TRANSCRIPT_CHUNK)
                    .build();
        }
    }

    // dex:group group-id:resolution group-label:"Resolution"
    // dex:explanation text:"Close the resolved ticket once no reply is left to record."
    static final class CloseResolvedTicket implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String reason) {
            return StepDecision.forceCompleteIfChannelsEmpty(
                    "resolved: " + reason,
                    StepMovement.of(AwaitTicketMessage.class, reason),
                    ticketReplies);
        }
    }

    // dex:group group-id:inactivity group-label:"Inactivity"
    // dex:explanation text:"Close the quiet ticket once no reply or resolution is waiting."
    static final class CloseInactiveTicket implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.forceCompleteIfChannelsEmpty(
                    "closed after inactivity",
                    StepMovement.of(AwaitTicketMessage.class, ""),
                    ticketReplies,
                    ticketResolutions);
        }
    }

    /** Appends replies to the current chunk and archives each full chunk. */
    static void appendToTranscript(final Context context, final List<String> texts) {
        final TranscriptChunk stored = transcriptChunks.get(context, CURRENT_TRANSCRIPT_CHUNK);
        TranscriptChunk chunk = stored != null ? stored : new TranscriptChunk(new ArrayList<TicketReply>());
        long nextSequence = chunk.replies.isEmpty()
                ? 1
                : chunk.replies.get(chunk.replies.size() - 1).sequence + 1;
        for (final String text : texts) {
            if (chunk.replies.size() == TRANSCRIPT_CHUNK_SIZE) {
                final String archiveToken = archiveChunkToken(chunk.replies.get(0).sequence);
                transcriptChunks.set(context, archiveToken, chunk);
                chunk = new TranscriptChunk(new ArrayList<TicketReply>());
            }
            chunk.replies.add(new TicketReply(nextSequence, text));
            nextSequence++;
        }
        transcriptChunks.set(context, CURRENT_TRANSCRIPT_CHUNK, chunk);
    }

    public static String archiveChunkToken(final long firstSequence) {
        return String.format("%020d", firstSequence);
    }

    public static class TicketReply {
        public long sequence;
        public String text;

        public TicketReply() {
        }

        public TicketReply(final long sequence, final String text) {
            this.sequence = sequence;
            this.text = text;
        }
    }

    public static class TranscriptChunk {
        public List<TicketReply> replies = new ArrayList<TicketReply>();

        public TranscriptChunk() {
        }

        public TranscriptChunk(final List<TicketReply> replies) {
            this.replies = replies;
        }
    }
}
