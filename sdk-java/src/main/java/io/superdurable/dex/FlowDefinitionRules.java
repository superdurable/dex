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

import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.format.DateTimeParseException;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.regex.Pattern;

/** Implements the cross-SDK Flow definition upload rules shared with every Dex SDK. */
final class FlowDefinitionRules {
    private static final Pattern RFC_3339 = Pattern.compile(
            "\\d{4}-\\d{2}-\\d{2}[Tt]\\d{2}:\\d{2}:\\d{2}(\\.\\d+)?([Zz]|[+-]\\d{2}:\\d{2})");
    private static final Pattern DRIVE_ROOT = Pattern.compile("[A-Za-z]:[\\\\/].*", Pattern.DOTALL);

    private FlowDefinitionRules() {
    }

    /**
     * Returns the strict RFC 3339 time truncated to whole milliseconds.
     *
     * @throws IllegalArgumentException if the value is not strict RFC 3339
     */
    static Instant parseGeneratedAt(final String value) {
        if (!RFC_3339.matcher(value).matches()) {
            throw new IllegalArgumentException("generatedAt " + value + " is not an RFC 3339 time");
        }
        try {
            return truncateToMillis(OffsetDateTime.parse(value.toUpperCase(Locale.ROOT)).toInstant());
        } catch (DateTimeParseException invalid) {
            throw new IllegalArgumentException("generatedAt " + value + " is not an RFC 3339 time", invalid);
        }
    }

    static Instant truncateToMillis(final Instant instant) {
        return Instant.ofEpochMilli(instant.toEpochMilli());
    }

    static boolean isAbsoluteSourcePath(final String path) {
        return path.startsWith("/") || path.startsWith("\\\\") || DRIVE_ROOT.matcher(path).matches();
    }

    /**
     * Returns the rewritten source path, or null when it is relative and stays unchanged.
     *
     * <p>A null working directory means it is unknown, so the base name is used.
     */
    static String relativeSourcePath(final String sourcePath, final String workingDirectory) {
        if (!isAbsoluteSourcePath(sourcePath)) {
            return null;
        }
        final List<String> path = lexicalSegments(sourcePath);
        if (workingDirectory != null && isAbsoluteSourcePath(workingDirectory)) {
            final List<String> directory = lexicalSegments(workingDirectory);
            if (path.size() > directory.size() && path.subList(0, directory.size()).equals(directory)) {
                return String.join("/", path.subList(directory.size(), path.size()));
            }
        }
        return path.size() > 1 ? path.get(path.size() - 1) : "";
    }

    /** Returns the root marker followed by the lexically normalized segments. */
    private static List<String> lexicalSegments(final String absolutePath) {
        final List<String> segments = new ArrayList<String>();
        final String rest;
        if (absolutePath.startsWith("\\\\")) {
            segments.add("//");
            rest = absolutePath.substring(2);
        } else if (absolutePath.startsWith("/")) {
            segments.add("/");
            rest = absolutePath.substring(1);
        } else {
            segments.add(absolutePath.substring(0, 1).toUpperCase(Locale.ROOT) + ":");
            rest = absolutePath.substring(3);
        }
        for (String segment : rest.split("[\\\\/]")) {
            if (segment.isEmpty() || ".".equals(segment)) {
                continue;
            }
            if ("..".equals(segment)) {
                if (segments.size() > 1) {
                    segments.remove(segments.size() - 1);
                }
                continue;
            }
            segments.add(segment);
        }
        return segments;
    }

    /** Encodes a JSON string escaping only quote, backslash, and control characters. */
    static String encodeJsonString(final String value) {
        final StringBuilder encoded = new StringBuilder(value.length() + 2).append('"');
        for (int index = 0; index < value.length(); index++) {
            final char character = value.charAt(index);
            switch (character) {
                case '"':
                    encoded.append("\\\"");
                    break;
                case '\\':
                    encoded.append("\\\\");
                    break;
                case '\b':
                    encoded.append("\\b");
                    break;
                case '\f':
                    encoded.append("\\f");
                    break;
                case '\n':
                    encoded.append("\\n");
                    break;
                case '\r':
                    encoded.append("\\r");
                    break;
                case '\t':
                    encoded.append("\\t");
                    break;
                default:
                    if (character < 0x20) {
                        encoded.append(String.format("\\u%04x", (int) character));
                    } else {
                        encoded.append(character);
                    }
                    break;
            }
        }
        return encoded.append('"').toString();
    }
}
