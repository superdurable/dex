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

import java.io.IOException;
import java.io.InputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.util.Locale;

final class NativeBlobCacheBridge {
    private static final String LIBRARY_PROPERTY = "dex.blobCache.nativeLibrary";

    static {
        try {
            final String path = System.getProperty(LIBRARY_PROPERTY);
            if (path == null || path.isEmpty()) {
                loadPackagedLibrary();
            } else {
                System.load(path);
            }
        } catch (IOException | UnsatisfiedLinkError failure) {
            throw new IllegalStateException(
                    "cannot load the Dex BlobCache native library for "
                            + System.getProperty("os.name") + " "
                            + System.getProperty("os.arch") + "; set -D" + LIBRARY_PROPERTY
                            + " to an absolute library path to override packaged natives",
                    failure);
        }
    }

    private NativeBlobCacheBridge() {
    }

    private static void loadPackagedLibrary() throws IOException {
        final String platform = platform();
        final String libraryName = libraryName();
        final String resource = "/META-INF/native/" + platform + "/" + libraryName;
        try (InputStream library = NativeBlobCacheBridge.class.getResourceAsStream(resource)) {
            if (library == null) {
                throw new IOException("native library resource is missing: " + resource);
            }
            final Path extractionDirectory = Files.createTempDirectory("dex-blob-cache-");
            extractionDirectory.toFile().deleteOnExit();
            final Path extractedLibrary = extractionDirectory.resolve(libraryName);
            Files.copy(library, extractedLibrary, StandardCopyOption.REPLACE_EXISTING);
            extractedLibrary.toFile().deleteOnExit();
            System.load(extractedLibrary.toAbsolutePath().toString());
        }
    }

    private static String platform() {
        return operatingSystem() + "-" + architecture();
    }

    private static String operatingSystem() {
        final String name = System.getProperty("os.name", "").toLowerCase(Locale.ROOT);
        if (name.contains("linux")) {
            return "linux";
        }
        if (name.contains("mac") || name.contains("darwin")) {
            return "macos";
        }
        if (name.contains("windows")) {
            return "windows";
        }
        throw new IllegalStateException("unsupported operating system: " + name);
    }

    private static String architecture() {
        final String name = System.getProperty("os.arch", "").toLowerCase(Locale.ROOT);
        if ("amd64".equals(name) || "x86_64".equals(name) || "x64".equals(name)) {
            return "x86_64";
        }
        if ("aarch64".equals(name) || "arm64".equals(name)) {
            return "aarch64";
        }
        throw new IllegalStateException("unsupported architecture: " + name);
    }

    private static String libraryName() {
        final String operatingSystem = operatingSystem();
        if ("macos".equals(operatingSystem)) {
            return "libdex_blob_cache_jni.dylib";
        }
        if ("windows".equals(operatingSystem)) {
            return "dex_blob_cache_jni.dll";
        }
        return "libdex_blob_cache_jni.so";
    }

    static native long cacheOpen(String directory, long maxBytes, long frequencyCounters);

    static native byte[] cacheGet(long handle, String blobId);

    static native boolean cachePut(long handle, String blobId, byte[] payload);

    static native void cacheDelete(long handle, String blobId);

    static native void cacheDeleteAll(long handle);

    static native void cacheClose(long handle);
}
