/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package io.superdurable.dex.integ.smoke;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStreamReader;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import java.util.stream.Collectors;

final class FlowSmokeHelper {
    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();
    private static final Pattern RUN_ID_PATTERN = Pattern.compile("runId\\s+(\\S+)");
    private static final Duration ASSERT_TIMEOUT = Duration.ofSeconds(10);
    private static volatile String dexcliPath;

    private FlowSmokeHelper() {
    }

    static void assertStartStep(
            final FlowSmokeEntry entry,
            final String flowId,
            final String runId) throws Exception {
        if (entry.flags.noStartStep) {
            return;
        }
        final long deadline = System.nanoTime() + ASSERT_TIMEOUT.toNanos();
        while (System.nanoTime() < deadline) {
            final JsonNode history = runDexcliFlowHistory(flowId, runId);
            final String startStepType = flowStartedStartStepType(history.path("events"));
            if (!startStepType.isEmpty()) {
                if (entry.flags.stepStartMayFail) {
                    return;
                }
                if (hasStartStepProgress(history.path("events"), startStepType)) {
                    return;
                }
                final JsonNode state = runDexcliFlowState(flowId, runId);
                if ("FLOW_STATUS_RUNNING".equals(state.path("flowStatus").asText())
                        && history.path("events").size() > 1) {
                    return;
                }
            }
            Thread.sleep(200L);
        }
        throw new AssertionError("start step did not succeed for " + entry.name);
    }

    static void assertNoUnexpectedFailures(
            final FlowSmokeEntry entry,
            final String flowId,
            final String runId) throws Exception {
        final JsonNode history = runDexcliFlowHistory(flowId, runId);
        for (final JsonNode event : history.path("events")) {
            final String type = event.path("type").asText();
            switch (type) {
                case "StepExecuteFailed", "StepWaitForFailed" -> {
                    if (!entry.flags.stepStartMayFail) {
                        throw new AssertionError(
                                "unexpected failure event for " + entry.name + ": " + type);
                    }
                }
                case "FlowClosed" -> {
                    if (isTerminalFlowClosedFailure(event.path("payload"))) {
                        if (entry.flags.stepStartMayFail && hasRetryRecovery(history.path("events"))) {
                            continue;
                        }
                        throw new AssertionError(
                                "unexpected terminal flow closure for "
                                        + entry.name
                                        + ": "
                                        + event.path("payload"));
                    }
                }
                default -> {
                }
            }
        }
        if (entry.flags.stepStartMayFail) {
            if (!hasRetryRecovery(history.path("events"))) {
                throw new AssertionError(
                        "expected retry recovery events for " + entry.name);
            }
        }
    }

    static FlowSmokeTriggerResult parseFlowTriggerResponse(
            final String body,
            final String workflowIdFromQuery) throws IOException {
        final String trimmed = body.trim();
        final JsonNode json = OBJECT_MAPPER.readTree(trimmed);
        if (json.hasNonNull("flowID")) {
            return new FlowSmokeTriggerResult(
                    json.path("flowID").asText(),
                    json.path("runID").asText(""));
        }
        if (json.hasNonNull("flowId")) {
            return new FlowSmokeTriggerResult(
                    json.path("flowId").asText(),
                    json.path("runId").asText(""));
        }
        final Matcher matcher = RUN_ID_PATTERN.matcher(trimmed);
        if (matcher.find()) {
            return new FlowSmokeTriggerResult(workflowIdFromQuery, matcher.group(1));
        }
        if (trimmed.startsWith("Started workflowId: ")) {
            return new FlowSmokeTriggerResult(trimmed.substring("Started workflowId: ".length()), "");
        }
        if (trimmed.startsWith("started workflowId: ")) {
            return new FlowSmokeTriggerResult(trimmed.substring("started workflowId: ".length()), "");
        }
        if (workflowIdFromQuery != null && !workflowIdFromQuery.isEmpty()) {
            return new FlowSmokeTriggerResult(workflowIdFromQuery, trimmed);
        }
        return new FlowSmokeTriggerResult("", trimmed);
    }

    private static JsonNode runDexcliFlowHistory(final String flowId, final String runId)
            throws Exception {
        final List<String> args = new ArrayList<>();
        args.add(dexcliPath());
        args.add("flow");
        args.add("history");
        args.add(flowId);
        args.add("--server");
        args.add(flowServiceAddress());
        args.add("--output");
        args.add("json");
        args.add("--page-size");
        args.add("50");
        if (runId != null && !runId.isEmpty()) {
            args.add("--run-id");
            args.add(runId);
        }
        return runDexcliJson(args);
    }

    private static JsonNode runDexcliFlowState(final String flowId, final String runId)
            throws Exception {
        final List<String> args = new ArrayList<>();
        args.add(dexcliPath());
        args.add("flow");
        args.add("state");
        args.add(flowId);
        args.add("--server");
        args.add(flowServiceAddress());
        args.add("--output");
        args.add("json");
        if (runId != null && !runId.isEmpty()) {
            args.add("--run-id");
            args.add(runId);
        }
        return runDexcliJson(args);
    }

    private static JsonNode runDexcliJson(final List<String> args) throws Exception {
        final ProcessBuilder builder = new ProcessBuilder(args);
        builder.redirectErrorStream(false);
        final Process process = builder.start();
        final String stdout;
        try (BufferedReader reader =
                new BufferedReader(
                        new InputStreamReader(process.getInputStream(), StandardCharsets.UTF_8))) {
            stdout = reader.lines().collect(Collectors.joining("\n"));
        }
        final String stderr;
        try (BufferedReader reader =
                new BufferedReader(
                        new InputStreamReader(process.getErrorStream(), StandardCharsets.UTF_8))) {
            stderr = reader.lines().collect(Collectors.joining("\n"));
        }
        if (!process.waitFor(30, TimeUnit.SECONDS)) {
            process.destroyForcibly();
            throw new IOException("dexcli timed out: " + String.join(" ", args));
        }
        if (process.exitValue() != 0) {
            throw new IOException(
                    "dexcli "
                            + String.join(" ", args.subList(1, args.size()))
                            + " failed: "
                            + stderr);
        }
        return OBJECT_MAPPER.readTree(stdout);
    }

    private static String flowStartedStartStepType(final JsonNode events) {
        for (final JsonNode event : events) {
            if (!"FlowStartedOrContinued".equals(event.path("type").asText())) {
                continue;
            }
            final JsonNode initialStart = event.path("payload").path("initialStart");
            final String startStepType = initialStart.path("startStepType").asText("");
            if (!startStepType.isEmpty()) {
                return startStepType;
            }
        }
        return "";
    }

    private static boolean hasStartStepProgress(final JsonNode events, final String startStepType) {
        for (final JsonNode event : events) {
            final String type = event.path("type").asText();
            if (!"StepWaitForCompleted".equals(type) && !"StepExecuteCompleted".equals(type)) {
                continue;
            }
            if (startStepType.equals(historyEventStepType(event.path("payload")))) {
                return true;
            }
        }
        return false;
    }

    private static String historyEventStepType(final JsonNode payload) {
        final String stepType = payload.path("stepType").asText("");
        if (!stepType.isEmpty()) {
            return stepType;
        }
        final String fromContext = payload.path("context").path("stepType").asText("");
        if (!fromContext.isEmpty()) {
            return fromContext;
        }
        return payload.path("input").path("stepType").asText("");
    }

    private static boolean isTerminalFlowClosedFailure(final JsonNode payload) {
        final JsonNode statusNode = payload.get("flowStatus");
        if (statusNode != null) {
            if (statusNode.isTextual()) {
                return switch (statusNode.asText()) {
                    case "FLOW_STATUS_COMPLETED",
                            "FLOW_STATUS_CONTINUED_AS_NEW",
                            "FLOW_STATUS_RUNNING",
                            "FLOW_STATUS_UNSPECIFIED",
                            "" -> false;
                    default -> true;
                };
            }
            if (statusNode.isNumber()) {
                final int status = statusNode.asInt();
                return status != 0 && status != 2 && status != 7;
            }
        }
        final String errorType = payload.path("errorType").asText("");
        return !errorType.isEmpty() && !"FLOW_ERROR_TYPE_UNSPECIFIED".equals(errorType);
    }

    private static boolean hasRetryRecovery(final JsonNode events) {
        boolean hasFailure = false;
        boolean hasRecovery = false;
        for (final JsonNode event : events) {
            final String type = event.path("type").asText();
            switch (type) {
                case "StepExecuteFailed", "StepWaitForFailed" -> hasFailure = true;
                case "StepExecuteCompleted", "StepWaitForCompleted" -> hasRecovery = true;
                default -> {
                }
            }
        }
        return hasFailure && hasRecovery;
    }

    private static String flowServiceAddress() {
        return System.getenv().getOrDefault("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801");
    }

    private static String dexcliPath() throws IOException, InterruptedException {
        if (dexcliPath != null) {
            return dexcliPath;
        }
        synchronized (FlowSmokeHelper.class) {
            if (dexcliPath != null) {
                return dexcliPath;
            }
            final String configured = System.getenv("DEXCLI_PATH");
            if (configured != null && !configured.trim().isEmpty()) {
                dexcliPath = configured.trim();
                return dexcliPath;
            }
            dexcliPath = buildDexcliBinary();
            return dexcliPath;
        }
    }

    private static String buildDexcliBinary() throws IOException, InterruptedException {
        final Path repoRoot = findRepoRoot();
        final Path binaryPath =
                Files.createTempFile("dexcli-java-flow-smoke-", "");
        Files.deleteIfExists(binaryPath);
        final Path outputPath = Path.of(binaryPath + ".bin");
        final ProcessBuilder builder =
                new ProcessBuilder(
                        "go",
                        "build",
                        "-trimpath",
                        "-o",
                        outputPath.toString(),
                        "./cmd/dexcli");
        builder.directory(repoRoot.resolve("cli").toFile());
        builder.environment().put("GOWORK", "off");
        final Process process = builder.start();
        final String output;
        try (BufferedReader reader =
                new BufferedReader(
                        new InputStreamReader(process.getInputStream(), StandardCharsets.UTF_8))) {
            output = reader.lines().collect(Collectors.joining("\n"));
        }
        if (process.waitFor(120, TimeUnit.SECONDS) && process.exitValue() == 0) {
            return outputPath.toString();
        }
        throw new IOException("build dexcli failed: " + output);
    }

    private static Path findRepoRoot() throws IOException {
        Path dir = Path.of(System.getProperty("user.dir")).toAbsolutePath();
        while (dir != null) {
            if (Files.exists(dir.resolve("cli/cmd/dexcli/main.go"))) {
                return dir;
            }
            dir = dir.getParent();
        }
        throw new IOException("find repository root from " + System.getProperty("user.dir"));
    }
}
