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

import io.superdurable.gen.ServerInfo;

final class ServerProtocolCompatibility {
    static final int MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION = 4;
    static final int MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION = 5;
    static final int HARD_DELETE_ATTRIBUTE_PROTOCOL_VERSION = 3;
    static final int NAMED_TASK_QUEUE_PROTOCOL_VERSION = 5;
    // The first server-zd release line that serves MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION.
    private static final String MINIMUM_SERVER_RELEASE = "server-zd newer than v0.1.16";

    private ServerProtocolCompatibility() {
    }

    static String sdkVersion() {
        final String implementationVersion = Worker.class.getPackage().getImplementationVersion();
        if (implementationVersion == null || implementationVersion.isEmpty()) {
            return "dev";
        }
        return implementationVersion;
    }

    static int negotiate(final ServerInfo serverInfo, final String sdkVersion) {
        if (serverInfo == null) {
            throw invalidServerInfo(sdkVersion, "unknown", 0, 0, "an empty response");
        }
        final int serverMinimum = serverInfo.getMinimumSupportedProtocolVersion();
        final int serverCurrent = serverInfo.getCurrentProtocolVersion();
        if (serverMinimum == 0 || serverCurrent == 0 || serverMinimum > serverCurrent) {
            throw invalidServerInfo(sdkVersion, serverInfo.getServerVersion(), serverMinimum,
                    serverCurrent, "an invalid protocol range");
        }
        if (serverCurrent < MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION) {
            throw new IncompatibleServerProtocolException(
                    "This SDK requires a Dex Server with protocol version "
                            + MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION + " or later ("
                            + MINIMUM_SERVER_RELEASE + "). " + describeServer(
                                    serverInfo.getServerVersion(), serverMinimum, serverCurrent)
                            + " Upgrade the Server before upgrading the SDK. "
                            + describeSdk(sdkVersion));
        }
        if (serverMinimum > MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION) {
            throw new IncompatibleServerProtocolException(
                    "The Dex Server requires protocol version " + serverMinimum
                            + " or later, newer than this SDK supports. " + describeServer(
                                    serverInfo.getServerVersion(), serverMinimum, serverCurrent)
                            + " Upgrade the Java SDK. " + describeSdk(sdkVersion));
        }
        return Math.min(serverCurrent, MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION);
    }

    // Reports a GetServerInfo transport failure without claiming an incompatibility.
    static IllegalStateException requestFailure(
            final String sdkVersion,
            final RuntimeException cause) {
        return new IllegalStateException(
                "cannot read the Dex Server protocol version: GetServerInfo failed; "
                        + describeSdk(sdkVersion),
                cause);
    }

    private static IllegalStateException invalidServerInfo(
            final String sdkVersion,
            final String serverVersion,
            final int serverMinimum,
            final int serverCurrent,
            final String problem) {
        return new IllegalStateException(
                "The Dex Server returned ServerInfo with " + problem + ". "
                        + describeServer(serverVersion, serverMinimum, serverCurrent) + " "
                        + describeSdk(sdkVersion));
    }

    private static String describeServer(
            final String serverVersion,
            final int serverMinimum,
            final int serverCurrent) {
        return "The Server reports protocol versions " + serverMinimum + "-" + serverCurrent
                + " (server version " + (serverVersion.isEmpty() ? "unknown" : serverVersion) + ").";
    }

    private static String describeSdk(final String sdkVersion) {
        return "Java SDK version \"" + sdkVersion + "\" supports protocol versions "
                + MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION + "-"
                + MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION + ".";
    }

    // Marks a definitive protocol mismatch, which a Client caches; other failures are retried.
    static final class IncompatibleServerProtocolException extends IllegalStateException {
        private static final long serialVersionUID = 1L;

        IncompatibleServerProtocolException(final String message) {
            super(message);
        }
    }
}
