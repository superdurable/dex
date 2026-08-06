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
