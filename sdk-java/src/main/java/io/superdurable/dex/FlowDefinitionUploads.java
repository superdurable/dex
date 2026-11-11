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

import com.fasterxml.jackson.core.JsonFactory;
import com.fasterxml.jackson.core.JsonParser;
import com.fasterxml.jackson.core.JsonToken;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.google.protobuf.ByteString;
import com.google.protobuf.Timestamp;
import io.grpc.Status;
import io.superdurable.gen.FlowDefinitionUpload;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.UploadFlowDefinitionsRequest;
import io.superdurable.gen.UploadFlowDefinitionsResponse;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.nio.file.InvalidPathException;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Set;
import java.util.concurrent.TimeUnit;
import java.util.logging.Level;
import java.util.logging.Logger;

/** Prepares Flow definition uploads and runs the Worker startup upload. */
final class FlowDefinitionUploads {
    static final int MAX_DEFINITIONS_PER_REQUEST = 256;
    // Half the Server's default 32 MiB gRPC message limit; a larger document goes alone.
    static final long MAX_CONTENT_BYTES_PER_REQUEST = 16L * 1024 * 1024;

    private static final Logger LOGGER = Logger.getLogger(FlowDefinitionUploads.class.getName());
    private static final JsonFactory JSON_FACTORY = new JsonFactory();
    private static final ObjectMapper JSON = new ObjectMapper();

    private FlowDefinitionUploads() {
    }

    static String uploader() {
        return "java/" + ServerProtocolCompatibility.sdkVersion();
    }

    /**
     * Uploads the registered Flow types' definitions; every failure is logged, never thrown.
     *
     * <p>Each request gets its own deadline of {@code timeout}.
     */
    static void uploadAtWorkerStart(
            final FlowServiceGrpc.FlowServiceBlockingStub service,
            final Credential credential,
            final Duration timeout,
            final List<FlowDefinitionDocument> documents,
            final Set<String> registeredFlowTypes) {
        final List<FlowDefinitionDocument> registered = new ArrayList<FlowDefinitionDocument>();
        for (FlowDefinitionDocument document : documents) {
            if (registeredFlowTypes.contains(document.getFlowType())) {
                registered.add(document);
            } else {
                LOGGER.info("skipping the Flow definition of Flow type " + document.getFlowType()
                        + " because this Worker does not register it");
            }
        }
        final List<FlowDefinitionDocument> prepared;
        try {
            prepared = rewriteSourcePaths(registered, workingDirectory());
        } catch (RuntimeException failure) {
            LOGGER.log(Level.WARNING, "cannot prepare Flow definitions for upload; skipping the upload",
                    failure);
            return;
        }
        for (UploadFlowDefinitionsRequest request : requests(prepared, false)) {
            final UploadFlowDefinitionsResponse response;
            try {
                response = GrpcCredential.callUnaryWithRetry(credential, () -> service
                        .withDeadlineAfter(timeout.toMillis(), TimeUnit.MILLISECONDS)
                        .uploadFlowDefinitions(request));
            } catch (RuntimeException failure) {
                logUploadFailure(failure);
                return;
            }
            logResults(request, response);
        }
    }

    /** Returns the documents with each absolute {@code source.path} made relative. */
    static List<FlowDefinitionDocument> rewriteSourcePaths(
            final List<FlowDefinitionDocument> documents,
            final Path workingDirectory) {
        final List<FlowDefinitionDocument> rewritten =
                new ArrayList<FlowDefinitionDocument>(documents.size());
        for (FlowDefinitionDocument document : documents) {
            final byte[] content = document.contentBytes();
            final byte[] rewrittenContent;
            try {
                rewrittenContent = rewriteSourcePath(content, workingDirectory);
            } catch (IOException invalid) {
                throw new IllegalArgumentException("Flow definition for Flow type "
                        + document.getFlowType() + " is not valid JSON", invalid);
            }
            rewritten.add(rewrittenContent == content ? document
                    : new FlowDefinitionDocument(document.getFlowType(), rewrittenContent,
                            document.getGeneratedAt()));
        }
        return rewritten;
    }

    /**
     * Returns content with each top-level {@code source.path} string made relative.
     *
     * <p>Only the bytes of that string change, so key order, spacing, and numbers stay exact.
     * Returns the same array when nothing changes.
     */
    static byte[] rewriteSourcePath(final byte[] content, final Path workingDirectory) throws IOException {
        final List<int[]> spans = new ArrayList<int[]>();
        final List<String> replacements = new ArrayList<String>();
        try (JsonParser parser = JSON_FACTORY.createParser(content)) {
            if (parser.nextToken() != JsonToken.START_OBJECT) {
                return content;
            }
            while (parser.nextToken() == JsonToken.FIELD_NAME) {
                final String field = parser.currentName();
                final JsonToken value = parser.nextToken();
                if ("source".equals(field) && value == JsonToken.START_OBJECT) {
                    collectSourcePathSpans(parser, workingDirectory, spans, replacements);
                } else {
                    parser.skipChildren();
                }
            }
        }
        if (spans.isEmpty()) {
            return content;
        }
        final ByteArrayOutputStream output = new ByteArrayOutputStream(content.length);
        int copied = 0;
        for (int index = 0; index < spans.size(); index++) {
            final int[] span = spans.get(index);
            output.write(content, copied, span[0] - copied);
            output.write(JSON.writeValueAsBytes(replacements.get(index)));
            copied = span[1];
        }
        output.write(content, copied, content.length - copied);
        return output.toByteArray();
    }

    private static void collectSourcePathSpans(
            final JsonParser parser,
            final Path workingDirectory,
            final List<int[]> spans,
            final List<String> replacements) throws IOException {
        while (parser.nextToken() == JsonToken.FIELD_NAME) {
            final String field = parser.currentName();
            final JsonToken value = parser.nextToken();
            if (!"path".equals(field) || value != JsonToken.VALUE_STRING) {
                parser.skipChildren();
                continue;
            }
            final long start = parser.currentTokenLocation().getByteOffset();
            final String sourcePath = parser.getText();
            final long end = parser.currentLocation().getByteOffset();
            final String relativePath = relativeSourcePath(sourcePath, workingDirectory);
            if (!relativePath.equals(sourcePath)) {
                spans.add(new int[] {(int) start, (int) end});
                replacements.add(relativePath);
            }
        }
    }

    /**
     * Returns an absolute path relative to the working directory when under it, else its file name.
     *
     * <p>A relative path is returned unchanged. Separators in the result are forward slashes.
     */
    static String relativeSourcePath(final String sourcePath, final Path workingDirectory) {
        final Path path;
        try {
            path = Paths.get(sourcePath);
        } catch (InvalidPathException invalid) {
            return baseName(sourcePath);
        }
        if (!path.isAbsolute()) {
            return sourcePath;
        }
        final Path normalized = path.normalize();
        if (normalized.startsWith(workingDirectory) && !normalized.equals(workingDirectory)) {
            return workingDirectory.relativize(normalized).toString().replace('\\', '/');
        }
        final Path fileName = normalized.getFileName();
        return fileName == null ? baseName(sourcePath) : fileName.toString();
    }

    private static String baseName(final String sourcePath) {
        final int separator = Math.max(sourcePath.lastIndexOf('/'), sourcePath.lastIndexOf('\\'));
        return sourcePath.substring(separator + 1);
    }

    static Path workingDirectory() {
        return Paths.get("").toAbsolutePath().normalize();
    }

    static List<UploadFlowDefinitionsRequest> requests(
            final List<FlowDefinitionDocument> documents,
            final boolean force) {
        final List<UploadFlowDefinitionsRequest> requests = new ArrayList<UploadFlowDefinitionsRequest>();
        UploadFlowDefinitionsRequest.Builder request = null;
        long requestContentBytes = 0;
        for (FlowDefinitionDocument document : documents) {
            final int contentBytes = document.contentBytes().length;
            if (request != null && (request.getDefinitionsCount() == MAX_DEFINITIONS_PER_REQUEST
                    || requestContentBytes + contentBytes > MAX_CONTENT_BYTES_PER_REQUEST)) {
                requests.add(request.build());
                request = null;
            }
            if (request == null) {
                request = UploadFlowDefinitionsRequest.newBuilder()
                        .setUploader(uploader())
                        .setForce(force);
                requestContentBytes = 0;
            }
            request.addDefinitions(FlowDefinitionUpload.newBuilder()
                    .setFlowType(document.getFlowType())
                    .setContent(ByteString.copyFrom(document.contentBytes()))
                    .setGeneratedAt(timestamp(document.getGeneratedAt())));
            requestContentBytes += contentBytes;
        }
        if (request != null) {
            requests.add(request.build());
        }
        return requests;
    }

    static List<FlowDefinitionUploadResult> results(final UploadFlowDefinitionsResponse response) {
        final List<FlowDefinitionUploadResult> results = new ArrayList<FlowDefinitionUploadResult>();
        for (io.superdurable.gen.FlowDefinitionUploadResult result : response.getResultsList()) {
            results.add(new FlowDefinitionUploadResult(
                    result.getFlowType(),
                    outcome(result.getOutcome()),
                    result.getStoredDigest(),
                    result.hasStoredGeneratedAt() ? instant(result.getStoredGeneratedAt()) : null));
        }
        return Collections.unmodifiableList(results);
    }

    private static FlowDefinitionUploadOutcome outcome(
            final io.superdurable.gen.FlowDefinitionUploadOutcome outcome) {
        switch (outcome) {
            case FLOW_DEFINITION_UPLOAD_OUTCOME_CREATED:
                return FlowDefinitionUploadOutcome.CREATED;
            case FLOW_DEFINITION_UPLOAD_OUTCOME_REPLACED:
                return FlowDefinitionUploadOutcome.REPLACED;
            case FLOW_DEFINITION_UPLOAD_OUTCOME_UNCHANGED:
                return FlowDefinitionUploadOutcome.UNCHANGED;
            case FLOW_DEFINITION_UPLOAD_OUTCOME_KEPT_EXISTING:
                return FlowDefinitionUploadOutcome.KEPT_EXISTING;
            default:
                return FlowDefinitionUploadOutcome.UNKNOWN;
        }
    }

    private static void logUploadFailure(final RuntimeException failure) {
        final Status status = Status.fromThrowable(failure);
        if (status.getCode() == Status.Code.UNIMPLEMENTED) {
            LOGGER.warning("the Dex Server does not support Flow definition upload; upgrade the Server"
                    + " to show these Flow definitions in Dex Web");
            return;
        }
        LOGGER.log(Level.WARNING, "cannot upload Flow definitions; the Worker continues without them: "
                + status.getCode() + (status.getDescription() == null ? "" : " " + status.getDescription()),
                failure);
    }

    private static void logResults(
            final UploadFlowDefinitionsRequest request,
            final UploadFlowDefinitionsResponse response) {
        final List<FlowDefinitionUploadResult> results = results(response);
        for (int index = 0; index < results.size(); index++) {
            final FlowDefinitionUploadResult result = results.get(index);
            switch (result.getOutcome()) {
                case CREATED:
                case REPLACED:
                    LOGGER.info("uploaded the Flow definition of Flow type " + result.getFlowType()
                            + ": " + result.getOutcome() + ", digest " + result.getStoredDigest());
                    break;
                case UNCHANGED:
                    LOGGER.fine("the stored Flow definition of Flow type " + result.getFlowType()
                            + " is unchanged, digest " + result.getStoredDigest());
                    break;
                default:
                    logKeptExisting(result, index < request.getDefinitionsCount()
                            ? request.getDefinitions(index) : null);
                    break;
            }
        }
    }

    private static void logKeptExisting(
            final FlowDefinitionUploadResult result,
            final FlowDefinitionUpload uploaded) {
        final String uploadedDescription = uploaded == null ? "unknown"
                : "digest " + sha256Hex(uploaded.getContent().toByteArray()) + " generated at "
                        + instant(uploaded.getGeneratedAt());
        LOGGER.warning("the Dex Server kept its stored Flow definition of Flow type " + result.getFlowType()
                + " (" + result.getOutcome() + "): stored digest " + result.getStoredDigest()
                + " generated at " + result.getStoredGeneratedAt() + ", uploaded "
                + uploadedDescription
                + "; regenerate the document or upload it with force to replace the stored one");
    }

    static String sha256Hex(final byte[] content) {
        final MessageDigest digest;
        try {
            digest = MessageDigest.getInstance("SHA-256");
        } catch (NoSuchAlgorithmException impossible) {
            throw new IllegalStateException("every Java runtime provides SHA-256", impossible);
        }
        final StringBuilder hex = new StringBuilder();
        for (byte value : digest.digest(content)) {
            hex.append(Character.forDigit((value >> 4) & 0xf, 16)).append(Character.forDigit(value & 0xf, 16));
        }
        return hex.toString();
    }

    private static Timestamp timestamp(final Instant instant) {
        return Timestamp.newBuilder()
                .setSeconds(instant.getEpochSecond())
                .setNanos(instant.getNano())
                .build();
    }

    private static Instant instant(final Timestamp timestamp) {
        return Instant.ofEpochSecond(timestamp.getSeconds(), timestamp.getNanos());
    }
}
