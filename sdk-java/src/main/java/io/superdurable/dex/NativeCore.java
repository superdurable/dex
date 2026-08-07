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
        try {
            final String path = System.getProperty(LIBRARY_PROPERTY);
            if (path == null || path.isEmpty()) {
                System.loadLibrary("dex_bridge_jni");
            } else {
                System.load(path);
            }
        } catch (UnsatisfiedLinkError failure) {
            throw new IllegalStateException(
                    "cannot load the Dex BlobCache native library; set -D"
                            + LIBRARY_PROPERTY + " to its absolute path",
                    failure);
        }
    }

    private NativeCore() {
    }

    static native long cacheOpen(String directory, long maxBytes, long frequencyCounters);

    static native byte[] cacheGet(long handle, String blobId);

    static native boolean cachePut(long handle, String blobId, byte[] payload);

    static native void cacheDelete(long handle, String blobId);

    static native void cacheDeleteAll(long handle);

    static native void cacheClose(long handle);
}
