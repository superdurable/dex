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

import io.grpc.Context;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.stub.StreamObserver;
import io.superdurable.gen.PollWorkerSlotKind;
import io.superdurable.gen.PollWorkerTaskPong;
import io.superdurable.gen.PollWorkerTaskRegister;
import io.superdurable.gen.PollWorkerTaskRequest;
import io.superdurable.gen.PollWorkerTaskResponse;
import io.superdurable.gen.WorkerTaskServiceGrpc;

import java.util.HashSet;
import java.util.Set;
import java.util.concurrent.Executors;
import java.util.concurrent.RejectedExecutionException;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

// Holds node slots under another app Worker ID and retakes any freed slot at once.
final class PullSlotCompetitor implements AutoCloseable {
    private final ManagedChannel channel;
    private final WorkerTaskServiceGrpc.WorkerTaskServiceStub service;
    private final String appWorkerID;
    private final ScheduledExecutorService loops = Executors.newSingleThreadScheduledExecutor(runnable -> {
        final Thread thread = new Thread(runnable, "pull-slot-competitor");
        thread.setDaemon(true);
        return thread;
    });
    // Guarded by this competitor's monitor.
    private final Set<Context.CancellableContext> held = new HashSet<Context.CancellableContext>();
    private boolean isStopped;

    PullSlotCompetitor(final String address, final String appWorkerID, final int loopCount) {
        this.channel = ManagedChannelBuilder.forTarget(address).usePlaintext().build();
        this.service = WorkerTaskServiceGrpc.newStub(channel);
        this.appWorkerID = appWorkerID;
        for (int loop = 0; loop < loopCount; loop++) {
            registerAndHold();
        }
    }

    // Each loop re-registers one millisecond after a rejection or a close.
    private void registerAndHold() {
        synchronized (this) {
            if (isStopped) {
                return;
            }
        }
        final Context.CancellableContext callContext = Context.ROOT.withCancellation();
        final Object streamLock = new Object();
        final StreamObserver<PollWorkerTaskRequest>[] requests = newRequestHolder();
        final StreamObserver<PollWorkerTaskResponse> responses = new StreamObserver<PollWorkerTaskResponse>() {
            @Override
            public void onNext(final PollWorkerTaskResponse response) {
                if (response.hasRegistered()) {
                    synchronized (PullSlotCompetitor.this) {
                        held.add(callContext);
                    }
                } else if (response.hasPing()) {
                    synchronized (streamLock) {
                        requests[0].onNext(PollWorkerTaskRequest.newBuilder()
                                .setPong(PollWorkerTaskPong.getDefaultInstance()).build());
                    }
                }
            }

            @Override
            public void onError(final Throwable failure) {
                ended();
            }

            @Override
            public void onCompleted() {
                ended();
            }

            private void ended() {
                callContext.cancel(null);
                synchronized (PullSlotCompetitor.this) {
                    held.remove(callContext);
                }
                try {
                    loops.schedule(PullSlotCompetitor.this::registerAndHold, 1L, TimeUnit.MILLISECONDS);
                } catch (RejectedExecutionException stopped) {
                    // The competitor stopped, so this loop ends.
                }
            }
        };
        synchronized (streamLock) {
            callContext.run(() -> requests[0] = service.pollWorkerTask(responses));
            requests[0].onNext(PollWorkerTaskRequest.newBuilder().setRegister(PollWorkerTaskRegister.newBuilder()
                    .setSupportsReservedSlotDispatch(true)
                    .setSlotKind(PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC)
                    .setAppWorkerId(appWorkerID)
                    .addFlowTypes("PullSlotCompetitorFlow")
                    .setSdkName("java")
                    .setSdkVersion(ServerProtocolCompatibility.sdkVersion())
                    .build()).build());
        }
    }

    @SuppressWarnings("unchecked")
    private static StreamObserver<PollWorkerTaskRequest>[] newRequestHolder() {
        return (StreamObserver<PollWorkerTaskRequest>[]) new StreamObserver<?>[1];
    }

    synchronized int heldStreams() {
        return held.size();
    }

    // Ends one held stream; its loop re-registers into the freed slot at once.
    void closeOneStream() {
        final Context.CancellableContext target;
        synchronized (this) {
            if (held.isEmpty()) {
                return;
            }
            target = held.iterator().next();
        }
        target.cancel(null);
    }

    @Override
    public void close() throws InterruptedException {
        final Set<Context.CancellableContext> streams;
        synchronized (this) {
            if (isStopped) {
                return;
            }
            isStopped = true;
            streams = new HashSet<Context.CancellableContext>(held);
        }
        loops.shutdownNow();
        for (Context.CancellableContext stream : streams) {
            stream.cancel(null);
        }
        channel.shutdownNow();
        channel.awaitTermination(10, TimeUnit.SECONDS);
    }
}
