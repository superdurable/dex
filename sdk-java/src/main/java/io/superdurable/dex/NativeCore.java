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

final class NativeCore {
    private static final String LIBRARY_PROPERTY = "dex.native.library";

    static {
        final String path = System.getProperty(LIBRARY_PROPERTY);
        if (path == null || path.isEmpty()) {
            System.loadLibrary("dex_bridge_jni");
        } else {
            System.load(path);
        }
    }

    private NativeCore() {
    }

    static native long create(String registryJson, int queueCapacity);

    static native void serve(long handle, String bindAddress);

    static native byte[] poll(long handle);

    static native void complete(
            long handle,
            int protocolVersion,
            long invocationId,
            boolean success,
            byte[] payload,
            String errorType,
            String errorMessage);

    static native void stop(long handle);

    static native void destroy(long handle);

    static native long cacheOpen(String directory, long maxBytes, long frequencyCounters);

    static native byte[] cacheGet(long handle, String blobId);

    static native boolean cachePut(long handle, String blobId, byte[] payload);

    static native void cacheDelete(long handle, String blobId);

    static native void cacheDeleteAll(long handle);

    static native void cacheClose(long handle);
}
