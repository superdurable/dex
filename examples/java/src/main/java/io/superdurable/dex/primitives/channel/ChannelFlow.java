/*
 * Copyright (c) 2026 Super Durable, Inc.
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

package io.superdurable.dex.primitives.channel;

import io.superdurable.dex.Channel;
import io.superdurable.dex.ChannelMessage;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.util.List;

@Component
public class ChannelFlow implements Flow<Integer> {
    public static final class QueuedMessageReference {
        public String messageId;

        public QueuedMessageReference() {
        }

        public QueuedMessageReference(final String messageId) {
            this.messageId = messageId;
        }
    }

    public static final class PendingMessage {
        public String messageId;
        public String value;

        public PendingMessage() {
        }

        public PendingMessage(final String messageId, final String value) {
            this.messageId = messageId;
            this.value = value;
        }
    }

    public static final class PendingMessages {
        public List<PendingMessage> messages;

        public PendingMessages() {
        }

        public PendingMessages(final List<PendingMessage> messages) {
            this.messages = messages;
        }
    }

    public final Channel<String> approvalMessages =
            Channel.define("ApprovalMessages", String.class);
    public final Channel<String> queuedMessages = Channel.define("QueuedMessages", String.class);
    public final Channel<String> prioritizedMessages =
            Channel.define("PrioritizedMessages", String.class);
    private final ChannelWaitStep waitForApproval = new ChannelWaitStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(waitForApproval);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(approvalMessages, queuedMessages, prioritizedMessages);
    }

    @RPC
    public void publishApprovalMessage(final Context context) {
        approvalMessages.publish(context, "approved");
    }

    @RPC
    public void enqueueChannelMessage(final Context context, final String value) {
        queuedMessages.publish(context, value);
    }

    @RPC(loadChannels = {"QueuedMessages"})
    public RPCResult<PendingMessages> getQueuedMessages(final Context context) {
        return RPCResult.of(new PendingMessages(toPendingMessages(queuedMessages, context)));
    }

    @RPC(isTransactional = true, loadChannels = {"QueuedMessages"})
    public void deleteQueuedMessage(
            final Context context,
            final QueuedMessageReference queuedMessageReference) {
        queuedMessages.delete(context, queuedMessageReference.messageId);
    }

    @RPC(loadChannels = {"PrioritizedMessages"})
    public RPCResult<PendingMessages> getPrioritizedMessages(final Context context) {
        return RPCResult.of(new PendingMessages(toPendingMessages(prioritizedMessages, context)));
    }

    @RPC(isTransactional = true, loadChannels = {"QueuedMessages"})
    public void moveQueuedMessageToPrioritizedMessages(
            final Context context,
            final QueuedMessageReference queuedMessageReference) {
        final ChannelMessage<String> messageToPrioritize =
                queuedMessages.findPendingMessage(context, queuedMessageReference.messageId);
        queuedMessages.delete(context, queuedMessageReference.messageId);
        if (messageToPrioritize != null) {
            prioritizedMessages.publish(context, messageToPrioritize.getValue());
        }
    }

    private List<PendingMessage> toPendingMessages(
            final Channel<String> channel,
            final Context context) {
        return channel.pendingMessages(context).stream()
                .map(message -> new PendingMessage(message.getMessageId(), message.getValue()))
                .toList();
    }

    final class ChannelWaitStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .addExecuteLoadChannel(queuedMessages)
                    .build();
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.anyOf(
                    approvalMessages.forOne(),
                    Timer.byDuration(Duration.ofSeconds(input)));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            final List<ChannelMessage<String>> pendingQueuedMessages =
                    queuedMessages.pendingMessages(context);
            if (!pendingQueuedMessages.isEmpty()) {
                queuedMessages.delete(context, pendingQueuedMessages.get(0).getMessageId());
                return StepDecision.gracefulComplete(pendingQueuedMessages.get(0).getValue());
            }
            if (context.hasTimerFired()) {
                return StepDecision.gracefulComplete("approval timed out");
            }
            final List<String> approvalMessageValues =
                    approvalMessages.getConditionResults(context);
            return StepDecision.gracefulComplete(approvalMessageValues.get(0));
        }
    }
}
