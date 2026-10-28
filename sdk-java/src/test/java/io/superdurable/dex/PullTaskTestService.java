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

import com.google.protobuf.Any;
import io.grpc.protobuf.StatusProto;
import io.grpc.stub.ServerCallStreamObserver;
import io.grpc.stub.StreamObserver;
import io.superdurable.gen.InvokeExecuteMethodOutput;
import io.superdurable.gen.InvokeExecuteMethodRequest;
import io.superdurable.gen.InvokeWaitForMethodOutput;
import io.superdurable.gen.InvokeWaitForMethodRequest;
import io.superdurable.gen.InvokeWorkerRPCRequest;
import io.superdurable.gen.InvokeWorkerRPCResponse;
import io.superdurable.gen.PollWorkerTask;
import io.superdurable.gen.PollWorkerTaskFrame;
import io.superdurable.gen.PollWorkerTaskRegistered;
import io.superdurable.gen.PollWorkerTaskRequest;
import io.superdurable.gen.PollWorkerTaskResponse;
import io.superdurable.gen.WorkerErrorResponse;
import io.superdurable.gen.WorkerTaskServiceGrpc;

import java.util.Iterator;
import java.util.NoSuchElementException;
import java.util.UUID;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;
import java.util.function.Function;

final class PullTaskTestService extends WorkerTaskServiceGrpc.WorkerTaskServiceImplBase {
    private final ConcurrentHashMap<String, BlockingQueue<TaskStream>> queues = new ConcurrentHashMap<>();

    @Override
    public StreamObserver<PollWorkerTaskRequest> pollWorkerTask(
            final StreamObserver<PollWorkerTaskResponse> responses) {
        final TaskStream stream = new TaskStream(responses);
        ((ServerCallStreamObserver<PollWorkerTaskResponse>) responses)
                .setOnCancelHandler(stream::closeStream);
        return new StreamObserver<PollWorkerTaskRequest>() {
            @Override
            public void onNext(final PollWorkerTaskRequest request) {
                if (request.hasRegister()) {
                    stream.queue = queues.computeIfAbsent(request.getRegister().getQueue(),
                            ignored -> new LinkedBlockingQueue<TaskStream>());
                    responses.onNext(PollWorkerTaskResponse.newBuilder().setRegistered(
                            PollWorkerTaskRegistered.getDefaultInstance()).build());
                    stream.queue.add(stream);
                } else if (request.hasTaskFrame()) {
                    final PollWorkerTaskFrame frame = request.getTaskFrame();
                    stream.task.isDone = isTerminal(frame);
                    stream.task.frames.add(frame);
                    if (isTerminal(frame) && !stream.isClosed) {
                        stream.isClosed = true;
                        stream.responses.onCompleted();
                    }
                } else if (request.hasReject()) {
                    stream.closeStream();
                    responses.onCompleted();
                }
            }

            @Override
            public void onError(final Throwable failure) {
                stream.closeStream();
            }

            @Override
            public void onCompleted() {
                stream.closeStream();
                responses.onCompleted();
            }
        };
    }

    Iterator<InvokeExecuteMethodOutput> invokeExecuteMethod(final InvokeExecuteMethodRequest request) {
        return invoke(request.getFlowType(), PollWorkerTask.newBuilder().setExecute(request),
                PollWorkerTaskFrame::getExecute);
    }

    Iterator<InvokeWaitForMethodOutput> invokeWaitForMethod(final InvokeWaitForMethodRequest request) {
        return invoke(request.getFlowType(), PollWorkerTask.newBuilder().setWaitFor(request),
                PollWorkerTaskFrame::getWaitFor);
    }

    InvokeWorkerRPCResponse invokeWorkerRPC(final InvokeWorkerRPCRequest request) {
        return invoke(request.getFlowType(), PollWorkerTask.newBuilder().setRpc(request),
                PollWorkerTaskFrame::getRpc).next();
    }

    private <Output> Iterator<Output> invoke(final String flowType, final PollWorkerTask.Builder task,
            final Function<PollWorkerTaskFrame, Output> output) {
        final TestTask invocation = new TestTask(flowType,
                task.setTaskId(UUID.randomUUID().toString()).build());
        assign(invocation);
        return new Iterator<Output>() {
            private PollWorkerTaskFrame pending;
            private boolean isDone;

            @Override
            public boolean hasNext() {
                if (pending != null) {
                    return true;
                }
                if (isDone) {
                    return false;
                }
                pending = await(invocation.frames);
                while (pending.equals(PollWorkerTaskFrame.getDefaultInstance())) {
                    assign(invocation);
                    pending = await(invocation.frames);
                }
                if (pending.hasWorkerError()) {
                    final WorkerErrorResponse error = pending.getWorkerError();
                    throw StatusProto.toStatusRuntimeException(com.google.rpc.Status.newBuilder()
                            .setCode(error.getGrpcStatus()).setMessage(error.getDetail())
                            .addDetails(Any.pack(error)).build());
                }
                return true;
            }

            @Override
            public Output next() {
                if (!hasNext()) {
                    throw new NoSuchElementException();
                }
                final PollWorkerTaskFrame frame = pending;
                pending = null;
                isDone = isTerminal(frame);
                return output.apply(frame);
            }
        };
    }

    private void assign(final TestTask task) {
        final BlockingQueue<TaskStream> queue = queues.computeIfAbsent(task.flowType,
                ignored -> new LinkedBlockingQueue<TaskStream>());
        TaskStream stream;
        do {
            stream = await(queue);
        } while (stream.isClosed);
        synchronized (stream) {
            stream.task = task;
            if (stream.isClosed) {
                task.frames.add(PollWorkerTaskFrame.getDefaultInstance());
                return;
            }
            stream.responses.onNext(PollWorkerTaskResponse.newBuilder().setTask(task.request).build());
        }
    }

    private static boolean isTerminal(final PollWorkerTaskFrame frame) {
        return frame.hasWorkerError() || frame.hasRpc()
                || frame.hasExecute() && frame.getExecute().hasResult()
                || frame.hasWaitFor() && frame.getWaitFor().hasResult();
    }

    private static <Value> Value await(final BlockingQueue<Value> queue) {
        try {
            final Value value = queue.poll(10, TimeUnit.SECONDS);
            if (value == null) {
                throw new AssertionError("pull task did not progress before timeout");
            }
            return value;
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            throw new AssertionError("interrupted awaiting pull task", interrupted);
        }
    }

    private static final class TaskStream {
        private final StreamObserver<PollWorkerTaskResponse> responses;
        private BlockingQueue<TaskStream> queue;
        private volatile TestTask task;
        private volatile boolean isClosed;

        private synchronized void closeStream() {
            if (isClosed) {
                return;
            }
            isClosed = true;
            if (queue != null) {
                queue.remove(this);
            }
            if (task != null && !task.isDone) {
                task.frames.add(PollWorkerTaskFrame.getDefaultInstance());
            }
        }

        private TaskStream(final StreamObserver<PollWorkerTaskResponse> responses) {
            this.responses = responses;
        }
    }

    private static final class TestTask {
        private final String flowType;
        private final PollWorkerTask request;
        private final BlockingQueue<PollWorkerTaskFrame> frames = new LinkedBlockingQueue<>();
        private volatile boolean isDone;

        private TestTask(final String flowType, final PollWorkerTask request) {
            this.flowType = flowType;
            this.request = request;
        }
    }
}
