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
import io.grpc.stub.StreamObserver;
import io.superdurable.gen.PollWorkerSlotKind;
import io.superdurable.gen.PollWorkerTaskRequest;

import java.time.Duration;

// Slot stream timings and test hooks; only tests change them, through PullWorkerOptions.Builder.
final class PullSlotSettings {
    // W, the shrink window of design decision D6.
    static final Duration DEFAULT_SHRINK_WINDOW = Duration.ofSeconds(30);

    private final Duration shrinkWindow;
    private final RegisterSendHook beforeRegisterSendForTest;
    private final RequestObserverWrapper requestObserverWrapperForTest;

    PullSlotSettings(final Duration shrinkWindow, final RegisterSendHook beforeRegisterSendForTest,
            final RequestObserverWrapper requestObserverWrapperForTest) {
        this.shrinkWindow = shrinkWindow;
        this.beforeRegisterSendForTest = beforeRegisterSendForTest;
        this.requestObserverWrapperForTest = requestObserverWrapperForTest;
    }

    static PullSlotSettings defaults() {
        return new PullSlotSettings(DEFAULT_SHRINK_WINDOW, null, null);
    }

    Duration getShrinkWindow() {
        return shrinkWindow;
    }

    RegisterSendHook getBeforeRegisterSendForTest() {
        return beforeRegisterSendForTest;
    }

    RequestObserverWrapper getRequestObserverWrapperForTest() {
        return requestObserverWrapperForTest;
    }

    // Runs outside the Worker monitor after an attempt's first admission and before its Register is re-checked.
    interface RegisterSendHook {
        void beforeRegisterSend(PollWorkerSlotKind slotKind);
    }

    // Wraps a poll call's request observer; callContext is the call that a reset cancels.
    interface RequestObserverWrapper {
        StreamObserver<PollWorkerTaskRequest> wrap(
                StreamObserver<PollWorkerTaskRequest> requests, Context.CancellableContext callContext);
    }
}
