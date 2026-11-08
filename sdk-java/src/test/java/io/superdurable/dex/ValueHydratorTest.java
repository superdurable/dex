/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Server;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.StreamObserver;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.LoadBlobRequestEntry;
import io.superdurable.gen.LoadBlobsRequest;
import io.superdurable.gen.LoadBlobsResponse;
import io.superdurable.gen.Value;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Optional;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class ValueHydratorTest {
    @Test
    void batches2500EntriesInInputOrder() throws Exception {
        final RecordingBlobService service = new RecordingBlobService();
        final Server server = NettyServerBuilder.forPort(0).addService(service).build().start();
        final ManagedChannel channel = ManagedChannelBuilder
                .forAddress("127.0.0.1", server.getPort()).usePlaintext().build();
        try {
            final ValueHydrator hydrator = new ValueHydrator(
                    FlowServiceGrpc.newBlockingStub(channel), new MissOnlyBlobCache());
            final List<Value> values = new ArrayList<Value>();
            for (int index = 0; index < 2500; index++) {
                values.add(Value.newBuilder().setInternalBlobIdForStringValue("blob-" + index).build());
            }
            final List<Value> hydrated = hydrator.hydrateAll("flow", values);
            assertEquals(Arrays.asList(1000, 1000, 500), service.batchSizes);
            for (int index = 0; index < 2500; index++) {
                assertEquals("blob-" + index, service.requestedBlobIds.get(index));
                assertEquals("value-blob-" + index, hydrated.get(index).getStringValue());
            }
        } finally {
            channel.shutdownNow();
            server.shutdownNow();
            assertTrue(channel.awaitTermination(10, TimeUnit.SECONDS));
            assertTrue(server.awaitTermination(10, TimeUnit.SECONDS));
        }
    }

    private static final class RecordingBlobService extends FlowServiceGrpc.FlowServiceImplBase {
        private final List<Integer> batchSizes = new ArrayList<Integer>();
        private final List<String> requestedBlobIds = new ArrayList<String>();

        @Override
        public void loadBlobs(final LoadBlobsRequest request,
                final StreamObserver<LoadBlobsResponse> observer) {
            batchSizes.add(request.getEntriesCount());
            final LoadBlobsResponse.Builder response = LoadBlobsResponse.newBuilder();
            for (LoadBlobRequestEntry entry : request.getEntriesList()) {
                final String blobId = entry.getBlobValue().getInternalBlobIdForStringValue();
                requestedBlobIds.add(blobId);
                response.putValues(blobId, Value.newBuilder().setStringValue("value-" + blobId).build());
            }
            observer.onNext(response.build());
            observer.onCompleted();
        }
    }

    private static final class MissOnlyBlobCache implements BlobCache {
        @Override
        public Optional<byte[]> get(final String blobId) {
            return Optional.empty();
        }

        @Override
        public boolean put(final String blobId, final byte[] payload) {
            return false;
        }

        @Override
        public void delete(final String blobId) { }

        @Override
        public void deleteAll() { }

        @Override
        public void close() { }
    }
}
