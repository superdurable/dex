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

package io.superdurable.dex.patterns.sequentiallychunkedattributemap;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCAttributeMapLock;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.StepList;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import org.springframework.stereotype.Component;

@Component
public class ChunkedSubscriberFlow implements Flow<Void> {
    public static final String FLOW_ID = "chunked-subscriber-archive";
    public static final String CURRENT_CHUNK_INSTANCE = "current";
    public static final int SUBSCRIBER_CHUNK_SIZE = 100;

    public final Attribute<SubscriberArchiveState> archiveState = Attribute.define(
            "subscriber_archive_state",
            SubscriberArchiveState.class);
    public final AttributeMap<SubscriberChunk> subscriberChunks = AttributeMap.define(
            "subscriber_chunks",
            SubscriberChunk.class);

    @Override
    public StepList<Void> getSteps() {
        return StepList.empty();
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(archiveState, subscriberChunks);
    }

    @RPC(
            name = "RegisterSubscriber",
            lockAttributes = {"subscriber_archive_state"},
            lockAttributeMaps = {
                @RPCAttributeMapLock(
                        attribute = "subscriber_chunks",
                        instance = CURRENT_CHUNK_INSTANCE)
            },
            loadAttributeMapInstances = {"subscriber_chunks/current"})
    public RPCResult<Subscriber> registerSubscriber(
            final Context context,
            final RegisterSubscriberInput input) {
        if (input.subscriberId == null || input.subscriberId.isEmpty()) {
            throw new IllegalArgumentException("subscriberId is required");
        }
        if (input.deliveryAddress == null || input.deliveryAddress.isEmpty()) {
            throw new IllegalArgumentException("deliveryAddress is required");
        }

        final SubscriberArchiveState state = archiveState.get(context);
        SubscriberChunk currentChunk = subscriberChunks.get(context, CURRENT_CHUNK_INSTANCE);
        final Subscriber subscriber = new Subscriber(
                state.nextSequence,
                input.subscriberId,
                input.deliveryAddress);
        if (currentChunk.subscribers.size() == SUBSCRIBER_CHUNK_SIZE) {
            final String archiveToken = archiveChunkToken(currentChunk.subscribers.get(0).sequence);
            subscriberChunks.set(context, archiveToken, currentChunk);
            state.latestArchivedChunkToken = archiveToken;
            currentChunk = new SubscriberChunk(new ArrayList<Subscriber>());
        }
        currentChunk.subscribers.add(subscriber);
        state.nextSequence++;
        subscriberChunks.set(context, CURRENT_CHUNK_INSTANCE, currentChunk);
        archiveState.set(context, state);
        return RPCResult.of(subscriber);
    }

    @RPC(name = "GetSubscriberPage")
    public RPCResult<SubscriberPage> getSubscriberPage(
            final Context context,
            final GetSubscriberPageInput input) {
        final String pageToken = validateSubscriberPageToken(input.pageToken);
        final SubscriberChunk chunk = subscriberChunks.get(context, pageToken);
        if (chunk == null) {
            throw new IllegalArgumentException("subscriber page \"" + pageToken + "\" not found");
        }
        final List<Subscriber> subscribers = new ArrayList<Subscriber>(chunk.subscribers);
        Collections.reverse(subscribers);
        String nextPageToken = "";
        if (!chunk.subscribers.isEmpty()) {
            if (CURRENT_CHUNK_INSTANCE.equals(pageToken)) {
                nextPageToken = archiveState.get(context).latestArchivedChunkToken;
            } else {
                final long firstSequence = Long.parseLong(pageToken);
                if (firstSequence > 1) {
                    nextPageToken = archiveChunkToken(firstSequence - SUBSCRIBER_CHUNK_SIZE);
                }
            }
        }
        return RPCResult.of(new SubscriberPage(pageToken, subscribers, nextPageToken));
    }

    public static String validateSubscriberPageToken(final String pageToken) {
        if (pageToken == null || pageToken.isEmpty()) {
            return CURRENT_CHUNK_INSTANCE;
        }
        if (CURRENT_CHUNK_INSTANCE.equals(pageToken)) {
            return pageToken;
        }
        if (pageToken.length() != 20 || !pageToken.matches("[0-9]{20}")) {
            throw new IllegalArgumentException("invalid page token \"" + pageToken + "\"");
        }
        final long firstSequence;
        try {
            firstSequence = Long.parseLong(pageToken);
        } catch (NumberFormatException error) {
            throw new IllegalArgumentException("invalid page token \"" + pageToken + "\"", error);
        }
        if (firstSequence == 0 || (firstSequence - 1) % SUBSCRIBER_CHUNK_SIZE != 0) {
            throw new IllegalArgumentException("invalid page token \"" + pageToken + "\"");
        }
        return pageToken;
    }

    public static String archiveChunkToken(final long firstSequence) {
        return String.format("%020d", firstSequence);
    }

    public static class Subscriber {
        public long sequence;
        public String subscriberId;
        public String deliveryAddress;

        public Subscriber() {
        }

        public Subscriber(
                final long sequence,
                final String subscriberId,
                final String deliveryAddress) {
            this.sequence = sequence;
            this.subscriberId = subscriberId;
            this.deliveryAddress = deliveryAddress;
        }
    }

    public static class SubscriberChunk {
        public List<Subscriber> subscribers;

        public SubscriberChunk() {
        }

        public SubscriberChunk(final List<Subscriber> subscribers) {
            this.subscribers = subscribers;
        }
    }

    public static class SubscriberArchiveState {
        public long nextSequence;
        public String latestArchivedChunkToken;

        public SubscriberArchiveState() {
        }

        public SubscriberArchiveState(
                final long nextSequence,
                final String latestArchivedChunkToken) {
            this.nextSequence = nextSequence;
            this.latestArchivedChunkToken = latestArchivedChunkToken;
        }
    }

    public static class RegisterSubscriberInput {
        public String subscriberId;
        public String deliveryAddress;

        public RegisterSubscriberInput() {
        }

        public RegisterSubscriberInput(
                final String subscriberId,
                final String deliveryAddress) {
            this.subscriberId = subscriberId;
            this.deliveryAddress = deliveryAddress;
        }
    }

    public static class GetSubscriberPageInput {
        public String pageToken;

        public GetSubscriberPageInput() {
        }

        public GetSubscriberPageInput(final String pageToken) {
            this.pageToken = pageToken;
        }
    }

    public static class SubscriberPage {
        public String pageToken;
        public List<Subscriber> subscribers;
        public String nextPageToken;

        public SubscriberPage() {
        }

        public SubscriberPage(
                final String pageToken,
                final List<Subscriber> subscribers,
                final String nextPageToken) {
            this.pageToken = pageToken;
            this.subscribers = subscribers;
            this.nextPageToken = nextPageToken;
        }
    }
}
