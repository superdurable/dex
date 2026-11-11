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

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class FlowDefinitionConformanceTest {
    private static final Path FIXTURE = Paths.get("..", "protos", "conformance", "flow_definition_upload.json");

    @Test
    void rewritesSourcePathsLikeEverySdk() throws Exception {
        final JsonNode cases = fixture().get("sourcePathRewrite");
        assertTrue(cases.size() > 0);
        for (JsonNode testCase : cases) {
            final String cwd = testCase.get("cwd").isNull() ? null : testCase.get("cwd").asText();
            final String expected = testCase.get("expected").isNull() ? null : testCase.get("expected").asText();
            assertEquals(expected,
                    FlowDefinitionRules.relativeSourcePath(testCase.get("sourcePath").asText(), cwd),
                    testCase.get("name").asText());
        }
    }

    @Test
    void encodesJsonStringsLikeEverySdk() throws Exception {
        for (JsonNode testCase : fixture().get("jsonStringEncoding")) {
            assertEquals(testCase.get("encoded").asText(),
                    FlowDefinitionRules.encodeJsonString(testCase.get("value").asText()));
        }
    }

    @Test
    void parsesGeneratedAtLikeEverySdk() throws Exception {
        for (JsonNode testCase : fixture().get("generatedAt")) {
            final String value = testCase.get("value").asText();
            if (testCase.path("error").asBoolean(false)) {
                assertThrows(IllegalArgumentException.class, () -> FlowDefinitionRules.parseGeneratedAt(value),
                        value);
            } else {
                assertEquals(testCase.get("unixMillis").asLong(),
                        FlowDefinitionRules.parseGeneratedAt(value).toEpochMilli(), value);
            }
        }
    }

    @Test
    void rewritesOnlyTheSourcePathBytes() throws Exception {
        final String content = "{ \"schemaVersion\":\"2.0\",\n \"source\" : {\"path\": \"/work/über/a.json\","
                + " \"language\":\"java\"}, \"n\": 1.50 }";
        final byte[] rewritten = FlowDefinitionUploads.rewriteSourcePath(
                content.getBytes(StandardCharsets.UTF_8), "/work");
        assertArrayEquals(content.replace("/work/über/a.json", "über/a.json").getBytes(StandardCharsets.UTF_8),
                rewritten);
        final byte[] relative = "{\"source\":{\"path\":\"a/b.json\"}}".getBytes(StandardCharsets.UTF_8);
        assertSame(relative, FlowDefinitionUploads.rewriteSourcePath(relative, "/work"));
    }

    @Test
    void mapsAnUnknownOutcomeWithoutFailing() {
        final io.superdurable.gen.UploadFlowDefinitionsResponse response =
                io.superdurable.gen.UploadFlowDefinitionsResponse.newBuilder()
                        .addResults(io.superdurable.gen.FlowDefinitionUploadResult.newBuilder()
                                .setFlowType("OrderFlow")
                                .setOutcomeValue(99))
                        .addResults(io.superdurable.gen.FlowDefinitionUploadResult.newBuilder()
                                .setFlowType("ReplyFlow"))
                        .build();
        assertEquals(FlowDefinitionUploadOutcome.UNKNOWN,
                FlowDefinitionUploads.results(response).get(0).getOutcome());
        assertEquals(FlowDefinitionUploadOutcome.UNKNOWN,
                FlowDefinitionUploads.results(response).get(1).getOutcome());
        assertNull(FlowDefinitionUploads.results(response).get(1).getStoredGeneratedAt());
    }

    private static JsonNode fixture() throws Exception {
        return new ObjectMapper().readTree(Files.readAllBytes(FIXTURE));
    }
}
