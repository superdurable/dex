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

import java.nio.ByteBuffer;
import java.nio.ByteOrder;

final class NativeInvocation {
    static final int WAIT_FOR = 1;
    static final int EXECUTE = 2;
    static final int WORKER_RPC = 3;
    private static final int HEADER_SIZE = 13;

    private final int protocolVersion;
    private final long id;
    private final int kind;
    private final byte[] request;

    private NativeInvocation(
            final int protocolVersion,
            final long id,
            final int kind,
            final byte[] request) {
        this.protocolVersion = protocolVersion;
        this.id = id;
        this.kind = kind;
        this.request = request;
    }

    static NativeInvocation decode(final byte[] envelope) {
        if (envelope == null || envelope.length < HEADER_SIZE) {
            throw new IllegalArgumentException("invalid native invocation envelope");
        }
        final ByteBuffer buffer = ByteBuffer.wrap(envelope).order(ByteOrder.LITTLE_ENDIAN);
        final int protocolVersion = buffer.getInt();
        final long id = buffer.getLong();
        final int kind = buffer.get() & 0xff;
        if (id <= 0 || kind < WAIT_FOR || kind > WORKER_RPC) {
            throw new IllegalArgumentException("invalid native invocation header");
        }
        final byte[] request = new byte[buffer.remaining()];
        buffer.get(request);
        return new NativeInvocation(protocolVersion, id, kind, request);
    }

    int getProtocolVersion() {
        return protocolVersion;
    }

    long getId() {
        return id;
    }

    int getKind() {
        return kind;
    }

    byte[] getRequest() {
        return request;
    }
}
