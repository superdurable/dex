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

public final class RpcDefinitions {
    private RpcDefinitions() {
    }

    @FunctionalInterface
    public interface RpcFunc1<I, O> {
        RPCResult<O> execute(Context context, I input);
    }

    @FunctionalInterface
    public interface RpcFunc0<O> {
        RPCResult<O> execute(Context context);
    }

    @FunctionalInterface
    public interface RpcProc1<I> {
        void execute(Context context, I input);
    }

    @FunctionalInterface
    public interface RpcProc0 {
        void execute(Context context);
    }
}
