/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex;

import java.io.Serializable;

/**
 * Provides serializable functional types for strongly typed RPC method references.
 *
 * <p>Applications normally encounter these types through {@link Client#invokeRPC}. Create a stub
 * with {@link Client#newRpcStub}, then pass a direct method reference; the SDK inspects the
 * serializable reference to identify the registered Flow instance and RPC method. Do not wrap the
 * reference in another lambda.
 *
 * <pre>{@code
 * OrderFlow stub = client.newRpcStub(OrderFlow.class, "order-123");
 * OrderStatus status = client.invokeRPC(stub::getStatus);
 * client.invokeRPC(stub::addNote, "ready to ship");
 * }</pre>
 */
public final class RpcDefinitions {
    private RpcDefinitions() {
    }

    /**
     * Represents an RPC function with one input and one output.
     *
     * @param <I> the concrete RPC input type
     * @param <O> the concrete RPC output type
     */
    @FunctionalInterface
    public interface RpcFunc1<I, O> extends Serializable {
        /**
         * Executes the RPC implementation on the worker.
         *
         * @param context the invocation-scoped Flow context
         * @param input the decoded RPC input
         * @return the typed RPC result
         */
        RPCResult<O> execute(Context context, I input);
    }

    /**
     * Represents an RPC function with no application input and one output.
     *
     * @param <O> the concrete RPC output type
     */
    @FunctionalInterface
    public interface RpcFunc0<O> extends Serializable {
        /**
         * Executes the RPC implementation on the worker.
         *
         * @param context the invocation-scoped Flow context
         * @return the typed RPC result
         */
        RPCResult<O> execute(Context context);
    }

    /**
     * Represents an RPC procedure with one input and no output.
     *
     * @param <I> the concrete RPC input type
     */
    @FunctionalInterface
    public interface RpcProc1<I> extends Serializable {
        /**
         * Executes the RPC implementation on the worker.
         *
         * @param context the invocation-scoped Flow context
         * @param input the decoded RPC input
         */
        void execute(Context context, I input);
    }

    /** Represents an RPC procedure with no application input or output. */
    @FunctionalInterface
    public interface RpcProc0 extends Serializable {
        /**
         * Executes the RPC implementation on the worker.
         *
         * @param context the invocation-scoped Flow context
         */
        void execute(Context context);
    }
}
