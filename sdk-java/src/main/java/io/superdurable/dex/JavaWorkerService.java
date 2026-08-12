/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex;

import com.google.protobuf.Any;
import io.grpc.Context;
import io.grpc.Status;
import io.grpc.protobuf.StatusProto;
import io.grpc.stub.StreamObserver;
import io.superdurable.dex.exceptions.RetryAfterException;
import io.superdurable.gen.InvokeExecuteMethodRequest;
import io.superdurable.gen.InvokeExecuteMethodResponse;
import io.superdurable.gen.InvokeWaitForMethodRequest;
import io.superdurable.gen.InvokeWaitForMethodResponse;
import io.superdurable.gen.InvokeWorkerRPCRequest;
import io.superdurable.gen.InvokeWorkerRPCResponse;
import io.superdurable.gen.WorkerErrorResponse;
import io.superdurable.gen.WorkerServiceGrpc;

import java.io.PrintWriter;
import java.io.StringWriter;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.Executor;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Future;
import java.util.concurrent.RejectedExecutionException;
import java.util.logging.Level;
import java.util.logging.Logger;

final class JavaWorkerService extends WorkerServiceGrpc.WorkerServiceImplBase {
    private static final Logger LOGGER = Logger.getLogger(JavaWorkerService.class.getName());
    private static final int MAX_STACK_TRACE_BYTES = 16 * 1024;
    private static final byte[] STACK_TRACE_TRUNCATION_MARKER =
            "\n... stack trace truncated by Dex Java SDK ..."
                    .getBytes(StandardCharsets.UTF_8);
    private static final Executor DIRECT_EXECUTOR = Runnable::run;

    private final WorkerDispatcher dispatcher;
    private final ExecutorService handlers;
    private final GrpcErrorStatusMapping grpcErrorStatusMapping;

    JavaWorkerService(
            final WorkerDispatcher dispatcher,
            final ExecutorService handlers,
            final GrpcErrorStatusMapping grpcErrorStatusMapping) {
        this.dispatcher = dispatcher;
        this.handlers = handlers;
        this.grpcErrorStatusMapping = grpcErrorStatusMapping;
    }

    @Override
    public void invokeWaitForMethod(
            final InvokeWaitForMethodRequest request,
            final StreamObserver<InvokeWaitForMethodResponse> observer) {
        submit(observer, () -> dispatcher.invokeWaitFor(request));
    }

    @Override
    public void invokeExecuteMethod(
            final InvokeExecuteMethodRequest request,
            final StreamObserver<InvokeExecuteMethodResponse> observer) {
        submit(observer, () -> dispatcher.invokeExecute(request));
    }

    @Override
    public void invokeWorkerRPC(
            final InvokeWorkerRPCRequest request,
            final StreamObserver<InvokeWorkerRPCResponse> observer) {
        submit(observer, () -> dispatcher.invokeRpc(request));
    }

    private <Response> void submit(
            final StreamObserver<Response> observer,
            final Invocation<Response> invocation) {
        final Context requestContext = Context.current();
        final HandlerCancellation cancellation = new HandlerCancellation(requestContext);
        final Runnable task = requestContext.wrap(() -> {
            try {
                invoke(observer, invocation);
            } finally {
                cancellation.complete();
            }
        });
        try {
            cancellation.attach(handlers.submit(task));
        } catch (RejectedExecutionException rejected) {
            cancellation.complete();
            observer.onError(Status.RESOURCE_EXHAUSTED
                    .withDescription("Java Worker handler capacity is exhausted")
                    .withCause(rejected)
                    .asRuntimeException());
        }
    }

    private <Response> void invoke(
            final StreamObserver<Response> observer,
            final Invocation<Response> invocation) {
        try {
            observer.onNext(invocation.invoke());
            observer.onCompleted();
        } catch (Throwable failure) {
            if (Context.current().isCancelled()) {
                observer.onError(Status.CANCELLED
                        .withDescription("Java Worker invocation canceled")
                        .withCause(failure)
                        .asRuntimeException());
                return;
            }
            LOGGER.log(Level.SEVERE, "Java Worker invocation failed", failure);
            observer.onError(mapFailure(failure));
        }
    }

    private Throwable mapFailure(final Throwable failure) {
        final RetryAfterException retryAfter = failure instanceof RetryAfterException
                ? (RetryAfterException) failure
                : null;
        final Throwable reportedFailure = retryAfter == null ? failure : retryAfter.getCause();
        final String message = reportedFailure.getMessage() == null
                ? reportedFailure.toString()
                : reportedFailure.getMessage();
        final WorkerErrorResponse.Builder details = WorkerErrorResponse.newBuilder()
                .setDetail(message)
                .setErrorType(reportedFailure.getClass().getName())
                .setStackTrace(stackTrace(reportedFailure));
        if (retryAfter != null) {
            details.setRetryAfterSeconds((int) retryAfter.getRetryAfter().getSeconds());
        }
        final com.google.rpc.Status status = com.google.rpc.Status.newBuilder()
                .setCode(grpcErrorStatusMapping.statusFor(reportedFailure).value())
                .setMessage(message)
                .addDetails(Any.pack(details.build()))
                .build();
        return StatusProto.toStatusRuntimeException(status);
    }

    private static String stackTrace(final Throwable failure) {
        final StringWriter buffer = new StringWriter();
        final PrintWriter writer = new PrintWriter(buffer);
        failure.printStackTrace(writer);
        writer.flush();
        return truncateStackTrace(buffer.toString());
    }

    private static String truncateStackTrace(final String value) {
        final byte[] encoded = value.getBytes(StandardCharsets.UTF_8);
        if (encoded.length <= MAX_STACK_TRACE_BYTES) {
            return value;
        }
        int prefixLength = MAX_STACK_TRACE_BYTES - STACK_TRACE_TRUNCATION_MARKER.length;
        while (prefixLength > 0 && (encoded[prefixLength] & 0xc0) == 0x80) {
            prefixLength--;
        }
        return new String(encoded, 0, prefixLength, StandardCharsets.UTF_8)
                + new String(STACK_TRACE_TRUNCATION_MARKER, StandardCharsets.UTF_8);
    }

    private interface Invocation<Response> {
        Response invoke() throws Throwable;
    }

    private static final class HandlerCancellation implements Context.CancellationListener {
        private final Context context;
        private Future<?> future;
        private boolean isCompleted;

        private HandlerCancellation(final Context context) {
            this.context = context;
            context.addListener(this, DIRECT_EXECUTOR);
        }

        synchronized void attach(final Future<?> value) {
            if (isCompleted) {
                value.cancel(true);
                return;
            }
            future = value;
            if (context.isCancelled()) {
                value.cancel(true);
            }
        }

        void complete() {
            synchronized (this) {
                isCompleted = true;
                future = null;
            }
            context.removeListener(this);
        }

        @Override
        public void cancelled(final Context ignored) {
            final Future<?> running;
            synchronized (this) {
                isCompleted = true;
                running = future;
                future = null;
            }
            context.removeListener(this);
            if (running != null) {
                running.cancel(true);
            }
        }
    }
}
