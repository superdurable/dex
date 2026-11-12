// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  ToolExecutionError,
  type AgentInterruptDisplay,
  type AgentTurnContext,
  type DeferredToolCall,
  type DeferredToolExecution,
  type DeferredToolOutcome,
  type SourceToolDefinition,
  type ToolEffect,
  type ToolSource,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "@superdurable/dex-agents";

import { isLocalToolGroupEnabled, isShellCommandApprovalRequired, type LocalToolGroup } from "../agent-harness.js";
import type { TurnConfig } from "../cli-config.js";
import { resolveTurnToolPolicy } from "../tool-policy.js";
import type { TurnConfigResolver } from "../turn-config-resolver.js";
import { buildEditFileDisplay, buildShellCommandDisplay, buildWriteFileDisplay } from "./approval-display.js";
import { BrowserSessions } from "./browser-sessions.js";
import { defaultShellTimeoutSeconds, maximumShellTimeoutSeconds, runShellCommand } from "./shell-command.js";
import { fetchWebPage } from "./web-page-fetch.js";
import { WorkspaceFiles } from "./workspace-files.js";

/**
 * Names of the local tools, by group. The names are part of the CLI contract: `approval.always` and session
 * settings refer to them.
 *
 * @since 0.5.0
 */
export const localToolNames = Object.freeze({
  files: Object.freeze(["read_file", "list_directory", "search_files", "write_file", "edit_file"]),
  shell: Object.freeze(["run_shell_command"]),
  web_fetch: Object.freeze(["fetch_web_page"]),
  browser: Object.freeze(["browser_navigate", "browser_snapshot", "browser_click", "browser_fill", "browser_submit_form"]),
} satisfies Record<LocalToolGroup, readonly string[]>);

/** One local tool: its group, definition, and default policy. */
interface LocalTool {
  readonly group: LocalToolGroup;
  readonly definition: SourceToolDefinition;
  readonly effect: ToolEffect;
  /** Whether a call waits for approval; the shell tool decides per command. */
  readonly requiresApproval: boolean;
}

const pathProperty = { type: "string", description: "A path relative to the workspace." };
const selectorProperty = {
  type: "string",
  description: 'A Playwright selector, such as role=button[name="Send"], text=Sign in, or the CSS selector #email.',
};

const localTools: readonly LocalTool[] = [
  {
    group: "files",
    effect: "read",
    requiresApproval: false,
    definition: {
      name: "read_file",
      description: "Read a UTF-8 text file in the workspace, up to 256 KiB.",
      inputSchema: objectSchema({ path: pathProperty }, ["path"]),
    },
  },
  {
    group: "files",
    effect: "read",
    requiresApproval: false,
    definition: {
      name: "list_directory",
      description: "List a directory in the workspace. Directories end with a slash.",
      inputSchema: objectSchema({ path: { ...pathProperty, description: "A directory; defaults to the workspace." } }, []),
    },
  },
  {
    group: "files",
    effect: "read",
    requiresApproval: false,
    definition: {
      name: "search_files",
      description: "Find the lines that contain a text in the files under a workspace directory.",
      inputSchema: objectSchema(
        {
          query: { type: "string", description: "The exact text to find." },
          path: { ...pathProperty, description: "The directory to search; defaults to the workspace." },
        },
        ["query"],
      ),
    },
  },
  {
    group: "files",
    effect: "idempotent_write",
    requiresApproval: true,
    definition: {
      name: "write_file",
      description: "Create or replace a UTF-8 text file in the workspace. The user approves each write.",
      inputSchema: objectSchema({ path: pathProperty, content: { type: "string", description: "The whole new content." } }, [
        "path",
        "content",
      ]),
    },
  },
  {
    group: "files",
    effect: "non_idempotent_write",
    requiresApproval: true,
    definition: {
      name: "edit_file",
      description:
        "Replace one exact occurrence of a text in an existing UTF-8 file in the workspace. old_text must occur exactly once. The user approves each edit.",
      inputSchema: objectSchema(
        {
          path: pathProperty,
          old_text: { type: "string", description: "The exact text to replace; it must occur once in the file." },
          new_text: { type: "string", description: "The replacement text." },
        },
        ["path", "old_text", "new_text"],
      ),
    },
  },
  {
    group: "shell",
    effect: "non_idempotent_write",
    requiresApproval: true,
    definition: {
      name: "run_shell_command",
      description:
        "Run a shell command in the workspace and return its exit status and output. The user approves each command unless it is on the allow list.",
      inputSchema: objectSchema(
        {
          command: { type: "string", description: "The command line." },
          timeout_seconds: {
            type: "integer",
            minimum: 1,
            maximum: maximumShellTimeoutSeconds,
            description: `Seconds before the command is killed; defaults to ${defaultShellTimeoutSeconds}.`,
          },
        },
        ["command"],
      ),
    },
  },
  {
    group: "web_fetch",
    effect: "read",
    requiresApproval: false,
    definition: {
      name: "fetch_web_page",
      description: "Fetch an http or https URL and return its text. HTML pages become plain text.",
      inputSchema: objectSchema({ url: { type: "string", description: "The URL." } }, ["url"]),
    },
  },
  {
    group: "browser",
    effect: "read",
    requiresApproval: false,
    definition: {
      name: "browser_navigate",
      description: "Open an http or https URL in the browser and return the page's accessibility snapshot as text.",
      inputSchema: objectSchema({ url: { type: "string", description: "The URL." } }, ["url"]),
    },
  },
  {
    group: "browser",
    effect: "read",
    requiresApproval: false,
    definition: {
      name: "browser_snapshot",
      description: "Return the open page's title, URL, and accessibility snapshot as text.",
      inputSchema: objectSchema({}, []),
    },
  },
  {
    group: "browser",
    effect: "non_idempotent_write",
    requiresApproval: true,
    definition: {
      name: "browser_click",
      description: "Click an element on the open page. The user approves each click.",
      inputSchema: objectSchema({ selector: selectorProperty }, ["selector"]),
    },
  },
  {
    group: "browser",
    effect: "non_idempotent_write",
    requiresApproval: true,
    definition: {
      name: "browser_fill",
      description: "Replace the value of an input on the open page. The user approves each fill.",
      inputSchema: objectSchema({ selector: selectorProperty, value: { type: "string", description: "The new value." } }, [
        "selector",
        "value",
      ]),
    },
  },
  {
    group: "browser",
    effect: "non_idempotent_write",
    requiresApproval: true,
    definition: {
      name: "browser_submit_form",
      description: "Submit the form that contains an element on the open page. The user approves each submission.",
      inputSchema: objectSchema({ selector: selectorProperty }, ["selector"]),
    },
  },
];

const localToolsByName: ReadonlyMap<string, LocalTool> = new Map(localTools.map((tool) => [tool.definition.name, tool]));
const disabledReferenceKey = "disabled_tool";

/**
 * The CLI's local tools, files, shell, web fetch, and browser, as a {@link ToolSource}. Each call runs in its own
 * Dex tool Step.
 *
 * Reads run at once. `write_file`, `edit_file`, and each browser click, fill, and submission wait for approval. Each shell
 * command waits for approval unless the turn's allow list has its exact text. The turn's `local_tools` switches
 * and `approval.always` apply to each model call. A file write's approval carries its workspace path and a unified
 * diff against the file as it is when the model asks, and a shell command's carries the working directory and the
 * full command (see `AgentInterrupt.display`).
 *
 * @since 0.5.0
 */
export class LocalToolSource implements ToolSource {
  /** The stable source name stored with each call. */
  public readonly sourceName = "local_tools";
  private readonly files: WorkspaceFiles;
  private readonly browser: BrowserSessions;

  /**
   * Creates the source.
   * @param workspace - The workspace's real path.
   * @param offeredGroups - The groups this command offers: the file's switched-on groups the harness does not
   * replace.
   * @param turns - The per-turn configuration.
   * @param log - Writes progress for the person at the terminal.
   */
  public constructor(
    private readonly workspace: string,
    private readonly offeredGroups: ReadonlySet<LocalToolGroup>,
    private readonly turns: TurnConfigResolver,
    log: (line: string) => void,
  ) {
    this.files = new WorkspaceFiles(workspace);
    this.browser = new BrowserSessions(log);
  }

  /**
   * Lists the tools of the groups that the running turn switches on.
   * @returns The tools.
   */
  public async listTools(): Promise<readonly SourceToolDefinition[]> {
    const turn = this.turns.currentTurn();
    return localTools.filter((tool) => this.isGroupOn(tool.group, turn)).map((tool) => tool.definition);
  }

  /**
   * Returns the tools the file switches on, without I/O.
   * @returns The tools.
   */
  public cachedTools(): readonly SourceToolDefinition[] {
    const turn = this.turns.currentTurn();
    return localTools.filter((tool) => this.isGroupOn(tool.group, turn)).map((tool) => tool.definition);
  }

  /**
   * Builds the deferred call with the tool's policy, the shell allow list, and `approval.always`.
   * @param turn - The `RunModel` context.
   * @param call - The model's call.
   * @returns The deferred call.
   */
  public deferredToolCall(
    turn: AgentTurnContext,
    call: { readonly toolCallId: string; readonly toolName: string; readonly arguments: string },
  ): DeferredToolCall {
    const turnConfig = this.turns.turnOf(turn);
    const tool = localToolsByName.get(call.toolName);
    if (tool === undefined || !this.isGroupOn(tool.group, turnConfig)) {
      return {
        toolCallId: call.toolCallId,
        toolName: call.toolName,
        arguments: call.arguments,
        policy: { effect: "read", requiresApproval: false },
        toolSourceReference: JSON.stringify({ [disabledReferenceKey]: call.toolName }),
      };
    }
    let requiresApproval = tool.requiresApproval;
    if (tool.group === "shell") {
      const command = readShellCommand(call.arguments);
      requiresApproval = command === undefined || isShellCommandApprovalRequired(turnConfig, command);
    }
    const policy = resolveTurnToolPolicy(turn, call.toolName, tool.effect, requiresApproval, turnConfig);
    const approvalDisplay = policy.requiresApproval ? this.approvalDisplay(call.toolName, call.arguments) : undefined;
    return {
      toolCallId: call.toolCallId,
      toolName: call.toolName,
      arguments: call.arguments,
      policy,
      ...(approvalDisplay === undefined ? {} : { approvalDisplay }),
    };
  }

  /**
   * What a file write or shell command acts on, for its approval: the file's workspace path with a diff against the
   * file now, or the working directory with the full command. Browser actions and calls with invalid arguments
   * have none.
   */
  private approvalDisplay(toolName: string, argumentsText: string): AgentInterruptDisplay | undefined {
    let toolArguments: Readonly<Record<string, unknown>>;
    try {
      toolArguments = parseArguments(argumentsText);
    } catch {
      return undefined;
    }
    const { path, content, old_text: oldText, new_text: newText, command } = toolArguments;
    switch (toolName) {
      case "write_file":
        return typeof path === "string" && typeof content === "string"
          ? buildWriteFileDisplay(this.files, path, content)
          : undefined;
      case "edit_file":
        return typeof path === "string" && typeof oldText === "string" && typeof newText === "string"
          ? buildEditFileDisplay(this.files, path, oldText, newText)
          : undefined;
      case "run_shell_command":
        return typeof command === "string" ? buildShellCommandDisplay(this.workspace, command) : undefined;
      default:
        return undefined;
    }
  }

  /**
   * Runs one call in its tool Step.
   * @param call - The approved call.
   * @returns The tool output.
   * @throws {ToolExecutionError} For invalid arguments, a path outside the workspace, or a failed operation.
   */
  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    const { toolName, toolSourceReference } = call.toolCall;
    if (toolSourceReference !== undefined && toolSourceReference !== null && toolSourceReference.includes(disabledReferenceKey)) {
      throw new ToolExecutionError(`tool ${toolName} is switched off for this session`);
    }
    const toolArguments = parseArguments(call.toolCall.arguments);
    await this.authorizeOnThisWorker(call, toolArguments);
    return { output: await this.runTool(toolName, toolArguments, call) };
  }

  /**
   * Checks the call again on the Worker that runs it, with this Worker's file and the turn's settings, because
   * another process with another configuration may have requested it. A refused call did not run.
   */
  private async authorizeOnThisWorker(
    call: DeferredToolExecution,
    toolArguments: Readonly<Record<string, unknown>>,
  ): Promise<void> {
    const { toolName } = call.toolCall;
    const tool = localToolsByName.get(toolName);
    let permissions: TurnConfig;
    try {
      permissions = await this.turns.resolveToolStep({ flowId: call.flowId, turnId: call.toolCall.turnId });
    } catch (error: unknown) {
      throw new ToolExecutionError(`tool ${toolName} did not run: the session's settings are rejected here: ${describe(error)}`, {
        cause: error,
      });
    }
    if (tool === undefined || !this.isGroupOn(tool.group, permissions)) {
      throw new ToolExecutionError(`tool ${toolName} is switched off in the configuration of the Worker that ran it; it did not run`);
    }
    if (call.toolCall.requiresApproval) {
      return;
    }
    if (permissions.approval.always.includes(toolName)) {
      throw new ToolExecutionError(`tool ${toolName} now needs approval, and this call had none; it did not run`);
    }
    const command = toolArguments.command;
    if (tool.group === "shell" && (typeof command !== "string" || isShellCommandApprovalRequired(permissions, command))) {
      throw new ToolExecutionError(
        "the command is not on the shell allow list of the Worker that ran it, and the call had no approval; it did not run",
      );
    }
  }

  /**
   * Local tools start no tasks.
   * @returns Never.
   * @throws {ToolExecutionError} Always.
   */
  public pollToolTask(_poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    return Promise.reject(new ToolExecutionError("local tools start no tasks"));
  }

  /**
   * Closes the browser.
   * @returns A Promise that resolves after it closed.
   */
  public close(): Promise<void> {
    return this.browser.close();
  }

  private async runTool(
    toolName: string,
    toolArguments: Readonly<Record<string, unknown>>,
    call: DeferredToolExecution,
  ): Promise<string> {
    const sessionId = call.flowId;
    switch (toolName) {
      case "read_file":
        return this.files.readFile(requireString(toolArguments, "path"));
      case "list_directory":
        return this.files.listDirectory(optionalString(toolArguments, "path") ?? ".");
      case "search_files":
        return this.files.searchFiles(requireString(toolArguments, "query"), optionalString(toolArguments, "path") ?? ".");
      case "write_file":
        return this.files.writeFile(requireString(toolArguments, "path"), requireString(toolArguments, "content"));
      case "edit_file":
        return this.files.editFile(
          requireString(toolArguments, "path"),
          requireString(toolArguments, "old_text"),
          requireString(toolArguments, "new_text"),
        );
      case "run_shell_command":
        return runShellCommand(
          requireString(toolArguments, "command"),
          this.workspace,
          readTimeoutSeconds(toolArguments),
          call.cancellationSignal,
        );
      case "fetch_web_page":
        return fetchWebPage(requireString(toolArguments, "url"), call.cancellationSignal);
      case "browser_navigate":
        return this.browser.navigate(sessionId, requireString(toolArguments, "url"));
      case "browser_snapshot":
        return this.browser.snapshot(sessionId);
      case "browser_click":
        return this.browser.click(sessionId, requireString(toolArguments, "selector"));
      case "browser_fill":
        return this.browser.fill(sessionId, requireString(toolArguments, "selector"), requireString(toolArguments, "value"));
      case "browser_submit_form":
        return this.browser.submitForm(sessionId, requireString(toolArguments, "selector"));
      default:
        throw new ToolExecutionError(`unknown local tool ${toolName}`);
    }
  }

  private isGroupOn(group: LocalToolGroup, turn: TurnConfig): boolean {
    return this.offeredGroups.has(group) && isLocalToolGroupEnabled(turn, group);
  }
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function objectSchema(properties: Record<string, unknown>, required: readonly string[]): Record<string, unknown> {
  return { type: "object", properties, required, additionalProperties: false };
}

function parseArguments(text: string): Readonly<Record<string, unknown>> {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch (error: unknown) {
    throw new ToolExecutionError("the arguments are not JSON", { cause: error });
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new ToolExecutionError("the arguments must be a JSON object");
  }
  return value as Record<string, unknown>;
}

function readShellCommand(argumentsText: string): string | undefined {
  try {
    const command = (JSON.parse(argumentsText) as { command?: unknown }).command;
    return typeof command === "string" ? command : undefined;
  } catch {
    return undefined;
  }
}

function requireString(toolArguments: Readonly<Record<string, unknown>>, name: string): string {
  const value = toolArguments[name];
  if (typeof value !== "string") {
    throw new ToolExecutionError(`argument ${name} must be a string`);
  }
  return value;
}

function optionalString(toolArguments: Readonly<Record<string, unknown>>, name: string): string | undefined {
  const value = toolArguments[name];
  if (value === undefined || value === null) {
    return undefined;
  }
  if (typeof value !== "string") {
    throw new ToolExecutionError(`argument ${name} must be a string`);
  }
  return value;
}

function readTimeoutSeconds(toolArguments: Readonly<Record<string, unknown>>): number {
  const value = toolArguments.timeout_seconds;
  if (value === undefined || value === null) {
    return defaultShellTimeoutSeconds;
  }
  if (typeof value !== "number" || !Number.isInteger(value) || value < 1 || value > maximumShellTimeoutSeconds) {
    throw new ToolExecutionError(`timeout_seconds must be an integer from 1 to ${maximumShellTimeoutSeconds}`);
  }
  return value;
}
