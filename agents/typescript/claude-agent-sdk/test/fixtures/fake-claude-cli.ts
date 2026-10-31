// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// A scripted Claude Code CLI for the tests; the README lists its protocol subset and commands.

import { randomUUID } from "node:crypto";
import { appendFileSync, existsSync, mkdirSync, readFileSync, readdirSync, realpathSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";

import { FileGate, InvocationLog } from "@superdurable/dex-agents/testing";

import {
  fakeClaudeInvocationLogName,
  fakeClaudeResumeWithoutHooksVariable,
  fakeClaudeStateDirectoryVariable,
} from "./fake-claude-cli-environment.js";

const cliVersion = "2.1.316";
const modelName = "claude-scripted";
const resumePrompt = "Continue from where you left off.";
const callAloneMarker = "again by itself";
const modelCallUsage = {
  input_tokens: 10,
  output_tokens: 5,
  cache_read_input_tokens: 3,
  cache_creation_input_tokens: 0,
} as const;
const hookDecisionPriority: Readonly<Record<string, number>> = { allow: 0, ask: 1, defer: 2, deny: 3 };
const valuedFlags = new Set([
  "--mcp-config",
  "--managed-settings",
  "--permission-mode",
  "--permission-prompt-tool",
  "--resume",
  "--session-id",
  "--settings",
]);

type JsonObject = Record<string, unknown>;

interface CliArguments {
  readonly resumeSessionId: string | undefined;
  readonly sessionId: string | undefined;
  readonly permissionPromptTool: string | undefined;
  readonly permissionMode: string;
  readonly hasSessionMirror: boolean;
  readonly isAwaitingInitialize: boolean;
  readonly mcpServers: Readonly<Record<string, JsonObject>>;
}

interface HookOutcome {
  readonly decision: string | undefined;
  readonly reason: string | undefined;
  readonly updatedInput: JsonObject | undefined;
}

interface ToolRequest {
  readonly requestIndex: number;
  readonly toolName: string;
  readonly toolInput: JsonObject;
}

interface ToolUse {
  readonly toolUseId: string;
  readonly cliToolName: string;
  readonly toolInput: JsonObject;
}

type ModelAction =
  | { readonly kind: "answer"; readonly text: string }
  | { readonly kind: "call_tools"; readonly requests: readonly ToolRequest[] }
  | { readonly kind: "pause_then_answer"; readonly gateName: string; readonly streamedText: string; readonly text: string };

class ScriptedModelFailure extends Error {}

class DeferredToolUnavailable extends Error {}

const noHookOutcome: HookOutcome = { decision: undefined, reason: undefined, updatedInput: undefined };

class StreamJsonChannel {
  private readonly pendingResponses = new Map<string, (response: JsonObject) => void>();
  private readonly userMessages: JsonObject[] = [];
  private userMessageWaiter: ((message: JsonObject | undefined) => void) | undefined;
  private initializeWaiter: ((request: JsonObject) => void) | undefined;
  private requestCount = 0;
  private isInputClosed = false;

  public start(): void {
    const lines = createInterface({ input: process.stdin, crlfDelay: Infinity });
    lines.on("line", (line) => {
      if (line.trim() !== "") {
        this.receive(JSON.parse(line) as JsonObject);
      }
    });
    lines.on("close", () => {
      this.isInputClosed = true;
      this.userMessageWaiter?.(undefined);
    });
  }

  public nextInitializeRequest(): Promise<JsonObject> {
    return new Promise((resolve) => {
      this.initializeWaiter = resolve;
    });
  }

  public nextUserMessage(): Promise<JsonObject | undefined> {
    const queued = this.userMessages.shift();
    if (queued !== undefined || this.isInputClosed) {
      return Promise.resolve(queued);
    }
    return new Promise((resolve) => {
      this.userMessageWaiter = (message) => {
        this.userMessageWaiter = undefined;
        resolve(message);
      };
    });
  }

  public emit(message: JsonObject): void {
    process.stdout.write(`${JSON.stringify(message)}\n`);
  }

  public respond(requestId: unknown, response: JsonObject): void {
    this.emit({
      type: "control_response",
      response: { subtype: "success", request_id: requestId, response },
    });
  }

  public async request(request: JsonObject): Promise<JsonObject> {
    this.requestCount += 1;
    const requestId = `fake_request_${this.requestCount}`;
    const response = await new Promise<JsonObject>((resolve) => {
      this.pendingResponses.set(requestId, resolve);
      this.emit({ type: "control_request", request_id: requestId, request });
    });
    if (response["subtype"] === "error") {
      throw new Error(`SDK answered ${String(request["subtype"])} with: ${String(response["error"])}`);
    }
    return (response["response"] as JsonObject | undefined) ?? {};
  }

  private receive(message: JsonObject): void {
    switch (message["type"]) {
      case "control_response": {
        const response = message["response"] as JsonObject;
        const resolve = this.pendingResponses.get(String(response["request_id"]));
        this.pendingResponses.delete(String(response["request_id"]));
        resolve?.(response);
        return;
      }
      case "control_request": {
        const request = message["request"] as JsonObject;
        if (request["subtype"] === "initialize" && this.initializeWaiter !== undefined) {
          this.initializeWaiter(message);
          this.initializeWaiter = undefined;
          return;
        }
        this.respond(message["request_id"], {});
        return;
      }
      case "user":
        if (this.userMessageWaiter !== undefined) {
          this.userMessageWaiter(message);
        } else {
          this.userMessages.push(message);
        }
        return;
      default:
        return;
    }
  }
}

class FakeTranscript {
  private constructor(
    public readonly path: string,
    public readonly sessionId: string,
    public readonly entries: JsonObject[],
    private readonly channel: StreamJsonChannel,
    private readonly hasSessionMirror: boolean,
  ) {}

  public static open(cliArguments: CliArguments, channel: StreamJsonChannel): FakeTranscript {
    const configDirectory = process.env["CLAUDE_CONFIG_DIR"] || join(homedir(), ".claude");
    const projectsDirectory = join(configDirectory, "projects");
    if (cliArguments.resumeSessionId === undefined) {
      const sessionId = cliArguments.sessionId ?? randomUUID();
      const projectKey = realpathSync(process.cwd()).replace(/[^a-zA-Z0-9]/g, "-");
      mkdirSync(join(projectsDirectory, projectKey), { recursive: true });
      const path = join(projectsDirectory, projectKey, `${sessionId}.jsonl`);
      return new FakeTranscript(path, sessionId, [], channel, cliArguments.hasSessionMirror);
    }
    const sessionId = cliArguments.resumeSessionId;
    const path = existsSync(projectsDirectory)
      ? readdirSync(projectsDirectory)
          .map((projectKey) => join(projectsDirectory, projectKey, `${sessionId}.jsonl`))
          .find((candidate) => existsSync(candidate))
      : undefined;
    if (path === undefined) {
      throw new Error(`No conversation found with session ID: ${sessionId}`);
    }
    const entries = readFileSync(path, "utf8")
      .split("\n")
      .filter((line) => line.trim() !== "")
      .map((line) => JSON.parse(line) as JsonObject);
    return new FakeTranscript(path, sessionId, entries, channel, cliArguments.hasSessionMirror);
  }

  public append(fields: JsonObject): JsonObject {
    const entry: JsonObject = {
      parentUuid: this.lastUuid(),
      isSidechain: false,
      userType: "external",
      cwd: process.cwd(),
      sessionId: this.sessionId,
      version: cliVersion,
      uuid: randomUUID(),
      timestamp: new Date().toISOString(),
      ...fields,
    };
    appendFileSync(this.path, `${JSON.stringify(entry)}\n`);
    this.entries.push(entry);
    if (this.hasSessionMirror) {
      this.channel.emit({ type: "transcript_mirror", filePath: this.path, entries: [entry] });
    }
    return entry;
  }

  /** Returns the deferred tool use at the end of the transcript, as `--resume` finds it. */
  public pendingDeferredToolUse(): ToolUse | undefined {
    for (let index = this.entries.length - 1; index >= 0; index -= 1) {
      const entry = this.entries[index] as JsonObject;
      const attachment = entry["attachment"] as JsonObject | undefined;
      if (entry["type"] !== "attachment" || attachment?.["type"] !== "hook_deferred_tool") {
        continue;
      }
      const toolUseId = String(attachment["toolUseID"]);
      const hasLaterResult = this.entries
        .slice(index + 1)
        .flatMap(contentBlocksOf)
        .some((block) => block["type"] === "tool_result" && block["tool_use_id"] === toolUseId);
      if (hasLaterResult) {
        return undefined;
      }
      return {
        toolUseId,
        cliToolName: String(attachment["toolName"]),
        toolInput: attachment["toolInput"] as JsonObject,
      };
    }
    return undefined;
  }

  public assistantEntryWithToolUse(toolUseId: string): JsonObject {
    const entry = findLastEntry(
      this.entries,
      (candidate) =>
        candidate["type"] === "assistant" &&
        contentBlocksOf(candidate).some((block) => block["type"] === "tool_use" && block["id"] === toolUseId),
    );
    if (entry === undefined) {
      throw new Error(`tool_use ${toolUseId} not found in transcript`);
    }
    return entry;
  }

  private lastUuid(): string | null {
    const entry = findLastEntry(this.entries, (candidate) => typeof candidate["uuid"] === "string");
    return entry === undefined ? null : String(entry["uuid"]);
  }
}

class ScriptedModel {
  public nextAction(entries: readonly JsonObject[]): ModelAction {
    const prompts = entries.flatMap((entry, index) => {
      const text = promptTextOf(entry);
      return text === undefined ? [] : [{ index, text }];
    });
    const lastPrompt = prompts.at(-1);
    if (lastPrompt === undefined) {
      throw new Error("the conversation has no prompt");
    }
    const command = lastPrompt.text;
    const tail = entries.slice(lastPrompt.index + 1);
    if (command.startsWith("say ")) {
      return { kind: "answer", text: `said: ${command.slice("say ".length)}` };
    }
    if (command === "recall") {
      return { kind: "answer", text: `history: ${prompts.slice(0, -1).map((prompt) => prompt.text).join(" | ")}` };
    }
    if (command === "fail") {
      throw new ScriptedModelFailure("scripted failure");
    }
    if (command.startsWith("stall ")) {
      const gateName = command.slice("stall ".length);
      const results = toolResultsByRequest(tail);
      const lookupResult = results.get(0);
      if (lookupResult === undefined) {
        return { kind: "call_tools", requests: [{ requestIndex: 0, toolName: "lookup", toolInput: { query: "stall" } }] };
      }
      return {
        kind: "pause_then_answer",
        gateName,
        streamedText: `thinking after ${lookupResult.text}`,
        text: `stalled at ${gateName}`,
      };
    }
    if (command.startsWith("call ")) {
      const [script = "", afterToolCalls = ""] = command.split(" ;; ");
      return this.nextToolAction(parseToolRequests(script), tail, afterToolCalls);
    }
    throw new Error(`unknown scripted command ${JSON.stringify(command)}`);
  }

  private nextToolAction(
    requests: readonly ToolRequest[],
    tail: readonly JsonObject[],
    afterToolCalls: string,
  ): ModelAction {
    const results = toolResultsByRequest(tail);
    if (results.size === 0) {
      return { kind: "call_tools", requests };
    }
    for (const request of requests) {
      if (results.get(request.requestIndex)?.text.includes(callAloneMarker) === true) {
        return { kind: "call_tools", requests: [request] };
      }
    }
    const summary = requests
      .map((request) => `${request.toolName}: ${results.get(request.requestIndex)?.text ?? "no result"}`)
      .join("; ");
    if (afterToolCalls === "fail") {
      throw new ScriptedModelFailure("scripted failure after tool calls");
    }
    if (afterToolCalls.startsWith("pause ")) {
      return { kind: "pause_then_answer", gateName: afterToolCalls.slice("pause ".length), streamedText: "", text: summary };
    }
    return { kind: "answer", text: summary };
  }
}

/** Talks JSON-RPC to SDK MCP servers over `mcp_message` and to HTTP MCP servers over POST. */
class FakeMcpClient {
  private readonly toolNamesByServer = new Map<string, readonly string[]>();
  private readonly httpServers = new Map<string, JsonObject>();
  private nextJsonRpcId = 0;

  public constructor(private readonly channel: StreamJsonChannel) {}

  public async connect(initializeRequest: JsonObject, configuredServers: Readonly<Record<string, JsonObject>>): Promise<void> {
    for (const [serverName, config] of Object.entries(configuredServers)) {
      if (config["type"] === "http") {
        this.httpServers.set(serverName, config);
        await this.send(serverName, "initialize", {
          protocolVersion: "2025-06-18",
          capabilities: {},
          clientInfo: { name: "fake-claude-cli", version: cliVersion },
        });
        const listed = await this.send(serverName, "tools/list", {});
        const tools = (listed["result"] as JsonObject)["tools"] as JsonObject[];
        this.toolNamesByServer.set(serverName, tools.map((listedTool) => String(listedTool["name"])));
      }
    }
    const serverNames = (initializeRequest["sdkMcpServers"] as string[] | undefined) ?? [];
    const manifests = (initializeRequest["sdkMcpServerManifests"] as Record<string, JsonObject> | undefined) ?? {};
    for (const serverName of serverNames) {
      const manifestTools = (manifests[serverName]?.["toolsListResult"] as JsonObject | undefined)?.["tools"];
      if (Array.isArray(manifestTools)) {
        this.toolNamesByServer.set(serverName, manifestTools.map((listedTool: JsonObject) => String(listedTool["name"])));
        continue;
      }
      await this.send(serverName, "initialize", {
        protocolVersion: "2025-06-18",
        capabilities: {},
        clientInfo: { name: "fake-claude-cli", version: cliVersion },
      });
      await this.channel.request({
        subtype: "mcp_message",
        server_name: serverName,
        message: { jsonrpc: "2.0", method: "notifications/initialized" },
      });
      const listed = await this.send(serverName, "tools/list", {});
      const tools = (listed["result"] as JsonObject)["tools"] as JsonObject[];
      this.toolNamesByServer.set(serverName, tools.map((listedTool) => String(listedTool["name"])));
    }
  }

  public cliToolName(toolName: string): string {
    for (const [serverName, toolNames] of this.toolNamesByServer) {
      if (toolNames.includes(toolName)) {
        return `mcp__${serverName}__${toolName}`;
      }
    }
    return toolName;
  }

  /** Like the real CLI, a resumed call finds only tools of `--mcp-config` SDK or `alwaysLoad` HTTP servers. */
  public isConnectedAtStartup(cliToolName: string, configuredServers: Readonly<Record<string, JsonObject>>): boolean {
    if (!cliToolName.startsWith("mcp__")) {
      return true;
    }
    const [, serverName = ""] = cliToolName.split("__");
    const config = configuredServers[serverName];
    return config?.["type"] === "sdk" || (config?.["type"] === "http" && config["alwaysLoad"] === true);
  }

  public async callTool(toolUse: ToolUse): Promise<{ readonly text: string; readonly isError: boolean }> {
    const [, serverName = "", toolName = ""] = toolUse.cliToolName.split("__");
    const response = await this.send(serverName, "tools/call", {
      name: toolName,
      arguments: toolUse.toolInput,
      _meta: { "claudecode/toolUseId": toolUse.toolUseId },
    });
    const error = response["error"] as JsonObject | undefined;
    if (error !== undefined) {
      return { text: String(error["message"]), isError: true };
    }
    const result = response["result"] as JsonObject;
    const texts = ((result["content"] as JsonObject[] | undefined) ?? [])
      .filter((block) => block["type"] === "text")
      .map((block) => String(block["text"]));
    return { text: texts.join("\n"), isError: result["isError"] === true };
  }

  private async send(serverName: string, method: string, params: JsonObject): Promise<JsonObject> {
    this.nextJsonRpcId += 1;
    const message = { jsonrpc: "2.0", id: this.nextJsonRpcId, method, params };
    const httpServer = this.httpServers.get(serverName);
    if (httpServer !== undefined) {
      const response = await fetch(String(httpServer["url"]), {
        method: "POST",
        headers: {
          ...(httpServer["headers"] as Record<string, string> | undefined),
          "content-type": "application/json",
          accept: "application/json, text/event-stream",
        },
        body: JSON.stringify(message),
      });
      if (!response.ok) {
        throw new Error(`HTTP MCP server ${serverName} answered ${method} with status ${response.status}`);
      }
      return (await response.json()) as JsonObject;
    }
    const answer = await this.channel.request({ subtype: "mcp_message", server_name: serverName, message });
    return answer["mcp_response"] as JsonObject;
  }
}

class FakeClaudeCli {
  private readonly channel = new StreamJsonChannel();
  private readonly model = new ScriptedModel();
  private readonly mcpClient = new FakeMcpClient(this.channel);
  private readonly stateDirectory: string;
  private readonly invocations: InvocationLog;
  private preToolUseMatchers: readonly JsonObject[] = [];
  private modelCallCount = 0;
  private transcript: FakeTranscript | undefined;

  // A test switch for the race a missing --await-initialize allows: hooks not yet registered.
  private readonly isResumingWithoutHooks = process.env[fakeClaudeResumeWithoutHooksVariable] === "1";

  public constructor(private readonly cliArguments: CliArguments) {
    const stateDirectory = process.env[fakeClaudeStateDirectoryVariable];
    if (stateDirectory === undefined || stateDirectory === "") {
      throw new Error(`${fakeClaudeStateDirectoryVariable} is not set`);
    }
    this.stateDirectory = stateDirectory;
    this.invocations = new InvocationLog(join(stateDirectory, fakeClaudeInvocationLogName));
  }

  public async run(): Promise<number> {
    this.channel.start();
    const initialize = await this.channel.nextInitializeRequest();
    const initializeRequest = initialize["request"] as JsonObject;
    const hooks = (initializeRequest["hooks"] as Record<string, JsonObject[]> | undefined) ?? {};
    this.preToolUseMatchers = hooks["PreToolUse"] ?? [];
    await this.mcpClient.connect(initializeRequest, this.cliArguments.mcpServers);
    this.channel.respond(initialize["request_id"], { commands: [], output_style: "default" });
    const isResume = this.cliArguments.resumeSessionId !== undefined;
    this.invocations.record(isResume ? "cli:resume" : "cli:new");
    this.transcript = FakeTranscript.open(this.cliArguments, this.channel);
    let exitCode = 0;
    try {
      // Like the real CLI in stream-json mode, a resumed deferred tool use runs without user input.
      const pending = this.transcript.pendingDeferredToolUse();
      if (pending !== undefined) {
        await this.runTurn(async () => this.resumeDeferredToolUse(pending));
      }
      for (;;) {
        const userMessage = await this.channel.nextUserMessage();
        if (userMessage === undefined) {
          break;
        }
        const content = (userMessage["message"] as JsonObject)["content"];
        await this.runTurn(async () => this.runPromptTurn(content));
      }
    } catch (error: unknown) {
      if (error instanceof ScriptedModelFailure) {
        this.emitResult({ isError: true, errors: [error.message] });
      } else if (!(error instanceof DeferredToolUnavailable)) {
        throw error;
      }
      exitCode = 1;
      await this.channel.nextUserMessage();
    }
    return exitCode;
  }

  private async runTurn(turnBody: () => Promise<void>): Promise<void> {
    this.modelCallCount = 0;
    this.emitSystemInit();
    await turnBody();
  }

  private async resumeDeferredToolUse(pending: ToolUse): Promise<void> {
    const transcript = this.requireTranscript();
    if (!this.mcpClient.isConnectedAtStartup(pending.cliToolName, this.cliArguments.mcpServers)) {
      this.emitResult({ isError: true, deferredUnavailable: true, deferredToolUse: toDeferredToolUse(pending) });
      throw new DeferredToolUnavailable();
    }
    const assistantEntry = transcript.assistantEntryWithToolUse(pending.toolUseId);
    // Like the real CLI, the resumed call starts from the original tool_use input and reruns every hook.
    const originalToolUse: ToolUse = { ...pending, toolInput: toolUseInputOf(assistantEntry, pending.toolUseId) };
    const hasRegisteredHooks = this.cliArguments.isAwaitingInitialize && !this.isResumingWithoutHooks;
    const deferred = await this.runToolUse(assistantEntry, originalToolUse, hasRegisteredHooks);
    if (deferred !== undefined) {
      this.emitDeferredResult(deferred);
      return;
    }
    transcript.append({ type: "user", isMeta: true, message: { role: "user", content: resumePrompt } });
    await this.runModelLoop();
  }

  private async runPromptTurn(promptContent: unknown): Promise<void> {
    this.requireTranscript().append({ type: "user", message: { role: "user", content: promptContent } });
    await this.runModelLoop();
  }

  private async runModelLoop(): Promise<void> {
    const transcript = this.requireTranscript();
    for (;;) {
      const action = this.model.nextAction(transcript.entries);
      if (action.kind === "answer") {
        this.answer(action.text);
        return;
      }
      if (action.kind === "pause_then_answer") {
        await this.pauseThenAnswer(action.gateName, action.streamedText, action.text);
        return;
      }
      const toolUses: ToolUse[] = action.requests.map((request) => ({
        toolUseId: `toolu_${request.requestIndex}_${randomUUID().replaceAll("-", "").slice(0, 12)}`,
        cliToolName: this.mcpClient.cliToolName(request.toolName),
        toolInput: request.toolInput,
      }));
      // Like the real CLI, every streamed content block becomes its own assistant message.
      const messageId = this.startModelCall();
      let lastDeferred: ToolUse | undefined;
      for (const toolUse of toolUses) {
        const assistantEntry = this.emitAssistantMessage(
          messageId,
          [{ type: "tool_use", id: toolUse.toolUseId, name: toolUse.cliToolName, input: toolUse.toolInput }],
          "tool_use",
        );
        lastDeferred = (await this.runToolUse(assistantEntry, toolUse, true)) ?? lastDeferred;
      }
      if (lastDeferred !== undefined) {
        this.emitDeferredResult(lastDeferred);
        return;
      }
    }
  }

  /** Runs one tool use the way the CLI does; returns the deferred call, with any updated input. */
  private async runToolUse(
    assistantEntry: JsonObject,
    toolUse: ToolUse,
    hasRegisteredHooks: boolean,
  ): Promise<ToolUse | undefined> {
    const outcome = hasRegisteredHooks ? await this.runPreToolUseHooks(toolUse) : noHookOutcome;
    const effectiveToolUse: ToolUse = { ...toolUse, toolInput: outcome.updatedInput ?? toolUse.toolInput };
    // The real CLI counts siblings per streamed content block, so every defer is honored.
    if (outcome.decision === "defer") {
      this.requireTranscript().append({
        type: "attachment",
        attachment: {
          type: "hook_deferred_tool",
          toolUseID: toolUse.toolUseId,
          toolName: toolUse.cliToolName,
          toolInput: effectiveToolUse.toolInput,
          hookName: `PreToolUse:${toolUse.cliToolName}`,
          hookEvent: "PreToolUse",
          permissionMode: this.cliArguments.permissionMode,
        },
      });
      return effectiveToolUse;
    }
    if (outcome.decision === "deny") {
      const reason = `PreToolUse:${toolUse.cliToolName} hook error: ${outcome.reason ?? "denied"}`;
      this.appendToolResult(assistantEntry, toolUse, reason, true);
      return undefined;
    }
    if (outcome.decision !== "allow") {
      const permission = await this.askPermission(effectiveToolUse);
      if (!permission.isPermitted) {
        this.appendToolResult(assistantEntry, toolUse, permission.message, true);
        return undefined;
      }
    }
    const output = await this.executeTool(effectiveToolUse);
    this.appendToolResult(assistantEntry, toolUse, output.text, output.isError);
    return undefined;
  }

  private async runPreToolUseHooks(toolUse: ToolUse): Promise<HookOutcome> {
    const transcript = this.requireTranscript();
    let decision: string | undefined;
    let reason: string | undefined;
    let updatedInput: JsonObject | undefined;
    // The real CLI runs matching hooks in parallel, so every hook sees the original input.
    for (const matcher of this.preToolUseMatchers) {
      const pattern = typeof matcher["matcher"] === "string" && matcher["matcher"] !== "" ? matcher["matcher"] : "*";
      if (pattern !== "*" && !new RegExp(`^(?:${pattern})$`).test(toolUse.cliToolName)) {
        continue;
      }
      for (const callbackId of (matcher["hookCallbackIds"] as string[] | undefined) ?? []) {
        const output = await this.channel.request({
          subtype: "hook_callback",
          callback_id: callbackId,
          input: {
            session_id: transcript.sessionId,
            transcript_path: transcript.path,
            cwd: process.cwd(),
            permission_mode: this.cliArguments.permissionMode,
            hook_event_name: "PreToolUse",
            tool_name: toolUse.cliToolName,
            tool_input: toolUse.toolInput,
            tool_use_id: toolUse.toolUseId,
          },
          tool_use_id: toolUse.toolUseId,
        });
        const specific = (output["hookSpecificOutput"] as JsonObject | undefined) ?? {};
        if (typeof specific["updatedInput"] === "object" && specific["updatedInput"] !== null) {
          updatedInput = specific["updatedInput"] as JsonObject;
        }
        const hookDecision = specific["permissionDecision"];
        if (
          typeof hookDecision === "string" &&
          (decision === undefined || (hookDecisionPriority[hookDecision] ?? 0) > (hookDecisionPriority[decision] ?? 0))
        ) {
          decision = hookDecision;
          reason = typeof specific["permissionDecisionReason"] === "string" ? specific["permissionDecisionReason"] : undefined;
        }
      }
    }
    return { decision, reason, updatedInput };
  }

  private async askPermission(toolUse: ToolUse): Promise<{ readonly isPermitted: boolean; readonly message: string }> {
    this.invocations.record(`can_use_tool ${toolUse.cliToolName}`);
    if (this.cliArguments.permissionMode === "bypassPermissions") {
      return { isPermitted: true, message: "" };
    }
    if (this.cliArguments.permissionPromptTool !== "stdio") {
      return { isPermitted: false, message: `Claude requested permissions to use ${toolUse.cliToolName}.` };
    }
    const answer = await this.channel.request({
      subtype: "can_use_tool",
      tool_name: toolUse.cliToolName,
      input: toolUse.toolInput,
      permission_suggestions: [],
      tool_use_id: toolUse.toolUseId,
    });
    if (answer["behavior"] === "allow") {
      return { isPermitted: true, message: "" };
    }
    return { isPermitted: false, message: String(answer["message"] ?? "") };
  }

  private async executeTool(toolUse: ToolUse): Promise<{ readonly text: string; readonly isError: boolean }> {
    if (toolUse.cliToolName.startsWith("mcp__")) {
      return this.mcpClient.callTool(toolUse);
    }
    if (toolUse.cliToolName === "Bash") {
      this.invocations.record("Bash");
      return { text: `ran ${String(toolUse.toolInput["command"])}`, isError: false };
    }
    return { text: `No such tool available: ${toolUse.cliToolName}`, isError: true };
  }

  private async pauseThenAnswer(gateName: string, streamedText: string, text: string): Promise<void> {
    if (streamedText !== "") {
      this.emitTextDelta(streamedText);
    }
    const gate = new FileGate(this.stateDirectory, gateName);
    gate.markReached();
    await gate.waitForRelease();
    this.answer(text);
  }

  private answer(text: string): void {
    const half = Math.floor(text.length / 2);
    for (const chunk of [text.slice(0, half), text.slice(half)]) {
      if (chunk !== "") {
        this.emitTextDelta(chunk);
      }
    }
    this.emitAssistantMessage(this.startModelCall(), [{ type: "text", text }], "end_turn");
    this.emitResult({ isError: false, resultText: text });
  }

  private startModelCall(): string {
    this.modelCallCount += 1;
    this.invocations.record("model_call");
    return `msg_${randomHex()}`;
  }

  private emitAssistantMessage(messageId: string, content: readonly JsonObject[], stopReason: string): JsonObject {
    const transcript = this.requireTranscript();
    const entry = transcript.append({
      type: "assistant",
      requestId: `req_${randomHex()}`,
      message: {
        id: messageId,
        type: "message",
        role: "assistant",
        model: modelName,
        content,
        stop_reason: stopReason,
        stop_sequence: null,
        usage: modelCallUsage,
      },
    });
    this.channel.emit({
      type: "assistant",
      message: entry["message"],
      parent_tool_use_id: null,
      session_id: transcript.sessionId,
      uuid: entry["uuid"],
    });
    return entry;
  }

  private appendToolResult(assistantEntry: JsonObject, toolUse: ToolUse, output: string, isError: boolean): void {
    const transcript = this.requireTranscript();
    const message = {
      role: "user",
      content: [{ type: "tool_result", tool_use_id: toolUse.toolUseId, content: output, is_error: isError }],
    };
    const entry = transcript.append({
      type: "user",
      message,
      toolUseResult: output,
      sourceToolAssistantUUID: assistantEntry["uuid"],
    });
    this.channel.emit({
      type: "user",
      message,
      parent_tool_use_id: null,
      session_id: transcript.sessionId,
      uuid: entry["uuid"],
      tool_use_result: output,
    });
  }

  private emitTextDelta(text: string): void {
    this.channel.emit({
      type: "stream_event",
      uuid: randomUUID(),
      session_id: this.requireTranscript().sessionId,
      event: { type: "content_block_delta", index: 0, delta: { type: "text_delta", text } },
      parent_tool_use_id: null,
    });
  }

  private emitSystemInit(): void {
    this.channel.emit({
      type: "system",
      subtype: "init",
      uuid: randomUUID(),
      session_id: this.requireTranscript().sessionId,
      apiKeySource: "none",
      claude_code_version: cliVersion,
      cwd: process.cwd(),
      tools: ["Bash"],
      mcp_servers: [],
      model: modelName,
      permissionMode: this.cliArguments.permissionMode,
      slash_commands: [],
      output_style: "default",
      skills: [],
      plugins: [],
    });
  }

  private emitDeferredResult(toolUse: ToolUse): void {
    this.emitResult({ isError: false, stopReason: "tool_deferred", deferredToolUse: toDeferredToolUse(toolUse) });
  }

  private emitResult(fields: {
    readonly isError: boolean;
    readonly resultText?: string;
    readonly stopReason?: string;
    readonly deferredToolUse?: JsonObject;
    readonly deferredUnavailable?: boolean;
    readonly errors?: readonly string[];
  }): void {
    const usage = Object.fromEntries(
      Object.entries(modelCallUsage).map(([key, value]) => [key, value * this.modelCallCount]),
    );
    this.channel.emit({
      type: "result",
      subtype: fields.isError && fields.deferredUnavailable !== true ? "error_during_execution" : "success",
      duration_ms: 1,
      duration_api_ms: 1,
      is_error: fields.isError,
      num_turns: this.modelCallCount,
      session_id: this.requireTranscript().sessionId,
      ...(fields.isError && fields.deferredUnavailable !== true
        ? { errors: fields.errors ?? [] }
        : { result: fields.resultText ?? "" }),
      stop_reason: fields.stopReason ?? "end_turn",
      total_cost_usd: 0,
      usage,
      modelUsage: {},
      permission_denials: [],
      uuid: randomUUID(),
      ...(fields.deferredToolUse === undefined
        ? {}
        : {
            terminal_reason: fields.deferredUnavailable === true ? "tool_deferred_unavailable" : "tool_deferred",
            deferred_tool_use: fields.deferredToolUse,
          }),
    });
  }

  private requireTranscript(): FakeTranscript {
    if (this.transcript === undefined) {
      throw new Error("the transcript is not open yet");
    }
    return this.transcript;
  }
}

function parseCliArguments(argumentList: readonly string[]): CliArguments {
  const values = new Map<string, string>();
  const flags = new Set<string>();
  const mcpServers: Record<string, JsonObject> = {};
  const recordValue = (name: string, value: string): void => {
    // Like the real CLI, every --mcp-config adds its servers.
    if (name === "--mcp-config") {
      Object.assign(mcpServers, (JSON.parse(value) as JsonObject)["mcpServers"] as Record<string, JsonObject>);
    }
    values.set(name, value);
  };
  for (let index = 0; index < argumentList.length; index += 1) {
    const argument = argumentList[index] ?? "";
    const equalsIndex = argument.indexOf("=");
    if (argument.startsWith("--") && equalsIndex > 0) {
      recordValue(argument.slice(0, equalsIndex), argument.slice(equalsIndex + 1));
    } else if (valuedFlags.has(argument) && index + 1 < argumentList.length) {
      recordValue(argument, argumentList[index + 1] ?? "");
      index += 1;
    } else {
      flags.add(argument);
    }
  }
  return {
    resumeSessionId: values.get("--resume"),
    sessionId: values.get("--session-id"),
    permissionPromptTool: values.get("--permission-prompt-tool"),
    permissionMode: values.get("--permission-mode") ?? "default",
    hasSessionMirror: flags.has("--session-mirror"),
    isAwaitingInitialize: flags.has("--await-initialize"),
    mcpServers,
  };
}

function parseToolRequests(script: string): ToolRequest[] {
  return script.split(" && ").map((call, requestIndex) => {
    const [, toolName = "", ...inputParts] = call.split(" ");
    return { requestIndex, toolName, toolInput: JSON.parse(inputParts.join(" ")) as JsonObject };
  });
}

/** Returns the latest result of each scripted request, keyed by request index. */
function toolResultsByRequest(tail: readonly JsonObject[]): Map<number, { readonly text: string; readonly isError: boolean }> {
  const resultsByToolUseId = new Map<string, { readonly text: string; readonly isError: boolean }>();
  for (const block of tail.flatMap(contentBlocksOf)) {
    if (block["type"] === "tool_result") {
      resultsByToolUseId.set(String(block["tool_use_id"]), {
        text: String(block["content"] ?? ""),
        isError: block["is_error"] === true,
      });
    }
  }
  const latest = new Map<number, { readonly text: string; readonly isError: boolean }>();
  for (const block of tail.flatMap(contentBlocksOf)) {
    const result = block["type"] === "tool_use" ? resultsByToolUseId.get(String(block["id"])) : undefined;
    if (result !== undefined) {
      latest.set(Number(String(block["id"]).split("_")[1]), result);
    }
  }
  return latest;
}

function promptTextOf(entry: JsonObject): string | undefined {
  if (entry["type"] !== "user" || entry["isMeta"] === true) {
    return undefined;
  }
  const content = (entry["message"] as JsonObject | undefined)?.["content"];
  if (typeof content === "string") {
    return content;
  }
  // Several blocks carry earlier unsent prompts; the command is the last one.
  if (Array.isArray(content) && content.length > 0 && content.every((block: JsonObject) => block["type"] === "text")) {
    return String((content.at(-1) as JsonObject)["text"]);
  }
  return undefined;
}

function contentBlocksOf(entry: JsonObject): JsonObject[] {
  const content = (entry["message"] as JsonObject | undefined)?.["content"];
  return Array.isArray(content) ? content.filter((block): block is JsonObject => typeof block === "object" && block !== null) : [];
}

function toolUseInputOf(assistantEntry: JsonObject, toolUseId: string): JsonObject {
  const block = contentBlocksOf(assistantEntry).find((candidate) => candidate["type"] === "tool_use" && candidate["id"] === toolUseId);
  return (block?.["input"] as JsonObject | undefined) ?? {};
}

function toDeferredToolUse(toolUse: ToolUse): JsonObject {
  return { id: toolUse.toolUseId, name: toolUse.cliToolName, input: toolUse.toolInput };
}

function findLastEntry(
  entries: readonly JsonObject[],
  predicate: (entry: JsonObject) => boolean,
): JsonObject | undefined {
  for (let index = entries.length - 1; index >= 0; index -= 1) {
    const entry = entries[index];
    if (entry !== undefined && predicate(entry)) {
      return entry;
    }
  }
  return undefined;
}

function randomHex(): string {
  return randomUUID().replaceAll("-", "").slice(0, 16);
}

process.exitCode = await new FakeClaudeCli(parseCliArguments(process.argv.slice(2))).run();
