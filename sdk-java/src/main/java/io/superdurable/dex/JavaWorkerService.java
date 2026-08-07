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
import io.superdurable.gen.InvokeExecuteMethodRequest;
import io.superdurable.gen.InvokeExecuteMethodResponse;
import io.superdurable.gen.InvokeWaitForMethodRequest;
import io.superdurable.gen.InvokeWaitForMethodResponse;
import io.superdurable.gen.InvokeWorkerRPCRequest;
import io.superdurable.gen.InvokeWorkerRPCResponse;
import io.superdurable.gen.WorkerErrorResponse;
import io.superdurable.gen.WorkerServiceGrpc;

import java.util.concurrent.ExecutorService;
import java.util.concurrent.RejectedExecutionException;
import java.util.logging.Level;
import java.util.logging.Logger;

final class JavaWorkerService extends WorkerServiceGrpc.WorkerServiceImplBase {
    private static final Logger LOGGER = Logger.getLogger(JavaWorkerService.class.getName());

    private final WorkerDispatcher dispatcher;
    private final ExecutorService handlers;

    JavaWorkerService(
            final WorkerDispatcher dispatcher,
            final ExecutorService handlers) {
        this.dispatcher = dispatcher;
        this.handlers = handlers;
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
        final Runnable task = Context.current().wrap(() -> invoke(observer, invocation));
        try {
            handlers.execute(task);
        } catch (RejectedExecutionException rejected) {
            observer.onError(Status.RESOURCE_EXHAUSTED
                    .withDescription("Java Worker handler capacity is exhausted")
                    .withCause(rejected)
                    .asRuntimeException());
        }
    }

    private static <Response> void invoke(
            final StreamObserver<Response> observer,
            final Invocation<Response> invocation) {
        try {
            observer.onNext(invocation.invoke());
            observer.onCompleted();
        } catch (Throwable failure) {
            LOGGER.log(Level.SEVERE, "Java Worker invocation failed", failure);
            observer.onError(mapFailure(failure));
        }
    }

    private static Throwable mapFailure(final Throwable failure) {
        final Status grpcStatus = Status.fromThrowable(failure);
        final String message = grpcStatus.getDescription() != null
                ? grpcStatus.getDescription()
                : failure.getMessage() == null
                ? failure.toString()
                : failure.getMessage();
        final WorkerErrorResponse details = WorkerErrorResponse.newBuilder()
                .setDetail(message)
                .setErrorType(failure.getClass().getName())
                .build();
        final com.google.rpc.Status status = com.google.rpc.Status.newBuilder()
                .setCode(grpcStatus.getCode().value())
                .setMessage(message)
                .addDetails(Any.pack(details))
                .build();
        return StatusProto.toStatusRuntimeException(status);
    }

    private interface Invocation<Response> {
        Response invoke();
    }
}
