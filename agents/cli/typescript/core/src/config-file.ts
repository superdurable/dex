// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, isAbsolute, join, resolve } from "node:path";

import { stringify } from "yaml";

import type { AgentHarness, LocalToolGroup } from "./agent-harness.js";
import { localToolGroups } from "./agent-harness.js";
import {
  type ConfigError,
  defaultWorkspace,
  parseConfigYaml,
  resolveConfig,
  type EffectiveConfig,
} from "./cli-config.js";

/**
 * A configuration a command runs with, and where it came from.
 *
 * @since 0.5.0
 */
export interface LoadedConfig {
  /** The effective configuration. */
  readonly effective: EffectiveConfig;
  /** The file's path; for in-memory defaults, the path `init` would write. */
  readonly configPath: string;
  /** Whether the configuration came from a file. */
  readonly isFromFile: boolean;
}

/**
 * The answers of the `init` questions, which become a configuration file.
 *
 * @since 0.5.0
 */
export interface InitAnswers {
  /** The model, or `null` to leave the field out and use the command's default. */
  readonly model: string | null;
  /** The instructions, or `null` for the harness default. */
  readonly systemPrompt: string | null;
  /** The enabled connector entries, such as `slack/support-slack`, or `null` for every authorized connection. */
  readonly connectors: readonly string[] | null;
  /** The workspace path, as written in the file. */
  readonly workspace: string;
  /**
   * Whether the file gets {@link workspace}. Defaults to `true`. It is `false` when a harness whose default workspace
   * is the launch directory takes the default, so the file has no `workspace` and the command keeps following the
   * directory it starts in.
   *
   * @since 0.6.0
   */
  readonly isWorkspaceWritten?: boolean;
  /** The local tool groups to switch on. */
  readonly localTools: ReadonlySet<LocalToolGroup>;
}

/** Asks one question and returns the answer, or `null` when input ended. */
export type AskQuestion = (question: string) => Promise<string | null>;

/**
 * Returns `~/.dex/agents/<harness name>.yaml`.
 * @param agentsDirectory - `~/.dex/agents`.
 * @param harness - The command's harness.
 * @returns The default configuration path.
 */
export function defaultConfigPath(agentsDirectory: string, harness: AgentHarness): string {
  return join(agentsDirectory, `${harness.name}.yaml`);
}

/**
 * Reads and resolves a configuration file.
 * @param configPath - The file.
 * @param harness - The command's harness.
 * @returns The configuration.
 * @throws {ConfigError} When the file is invalid.
 */
export function readConfigFile(configPath: string, harness: AgentHarness): LoadedConfig {
  const value = parseConfigYaml(readFileSync(configPath, "utf8"));
  return { effective: resolveHarnessConfig(value, harness), configPath, isFromFile: true };
}

/**
 * Returns whether a configuration file exists.
 * @param configPath - The file.
 * @returns `true` when it exists.
 */
export function isConfigPresent(configPath: string): boolean {
  return existsSync(configPath);
}

/**
 * Returns the answers that `--yes` and non-interactive runs use: the command's default model, the harness
 * instructions, every authorized connection, the harness's default workspace, and every local tool group. For a
 * harness whose default workspace is the launch directory, the workspace is that directory and is not written.
 * @param harness - The command's harness.
 * @returns The default answers.
 */
export function defaultInitAnswers(harness: AgentHarness): InitAnswers {
  const isLaunchDirectory = harness.defaultWorkspaceLocation === "launch_directory";
  return {
    model: harness.defaultModel,
    systemPrompt: null,
    connectors: null,
    workspace: isLaunchDirectory ? process.cwd() : defaultWorkspace,
    ...(isLaunchDirectory ? { isWorkspaceWritten: false } : {}),
    localTools: new Set(localToolGroups),
  };
}

/**
 * Asks the `init` questions. An empty answer takes the default shown in brackets.
 * @param harness - The command's harness.
 * @param ask - Asks one question.
 * @param log - Writes notes for the person at the terminal.
 * @returns The answers, or `null` when input ended.
 */
export async function askInitQuestions(
  harness: AgentHarness,
  ask: AskQuestion,
  log: (line: string) => void,
): Promise<InitAnswers | null> {
  const defaults = defaultInitAnswers(harness);
  log(`Creating the configuration of dex-agents-${harness.name}. Press Enter to take the default in brackets.`);
  let model: string | null = null;
  for (;;) {
    const answer = await ask(`Model${harness.defaultModel === null ? "" : ` [${harness.defaultModel}]`}: `);
    if (answer === null) {
      return null;
    }
    model = answer.trim() === "" ? harness.defaultModel : answer.trim();
    if (model !== null) {
      break;
    }
    log("This command has no default model; enter one.");
  }
  const prompt = await ask("System prompt [the harness default]: ");
  if (prompt === null) {
    return null;
  }
  const connectors = await ask("Connectors, comma-separated, such as slack/support-slack, hackernews [every authorized connection]: ");
  if (connectors === null) {
    return null;
  }
  const workspaceDefault =
    defaults.isWorkspaceWritten === false ? `the directory the command starts in, ${defaults.workspace}` : defaults.workspace;
  const workspace = await ask(`Workspace for local tools [${workspaceDefault}]: `);
  if (workspace === null) {
    return null;
  }
  const tools = await ask(`Local tools, comma-separated [${localToolGroups.join(", ")}] (or "none"): `);
  if (tools === null) {
    return null;
  }
  log("Put provider API keys in environment variables, never in the configuration file.");
  return {
    model: model === harness.defaultModel ? harness.defaultModel : model,
    systemPrompt: prompt.trim() === "" ? null : prompt.trim(),
    connectors: connectors.trim() === "" ? null : splitList(connectors),
    ...(workspace.trim() === ""
      ? {
          workspace: defaults.workspace,
          ...(defaults.isWorkspaceWritten === undefined ? {} : { isWorkspaceWritten: defaults.isWorkspaceWritten }),
        }
      : { workspace: workspace.trim() }),
    localTools: parseLocalToolGroups(tools),
  };
}

/**
 * Writes a configuration file from answers and returns its configuration.
 * @param configPath - The file to write; missing directories are created.
 * @param answers - The answers.
 * @param harness - The command's harness.
 * @returns The configuration read back from the file.
 * @throws {ConfigError} When the answers do not make a valid file, such as an invalid connector name.
 */
export function writeConfigFile(configPath: string, answers: InitAnswers, harness: AgentHarness): LoadedConfig {
  const text = renderConfigYaml(answers, harness);
  const effective = resolveHarnessConfig(parseConfigYaml(text), harness);
  mkdirSync(dirname(configPath), { recursive: true });
  writeFileSync(configPath, text);
  return { effective, configPath, isFromFile: true };
}

/**
 * Returns the configuration of answers without writing a file.
 * @param configPath - The path `init` would write.
 * @param answers - The answers.
 * @param harness - The command's harness.
 * @returns The configuration.
 * @throws {ConfigError} When the command has no default model.
 */
export function inMemoryConfig(configPath: string, answers: InitAnswers, harness: AgentHarness): LoadedConfig {
  const effective = resolveHarnessConfig(parseConfigYaml(renderConfigYaml(answers, harness)), harness);
  return { effective, configPath, isFromFile: false };
}

/**
 * Expands a configuration path: a leading `~` is the home directory, and a relative path is relative to the
 * configuration file's directory.
 * @param path - The path as written.
 * @param configPath - The configuration file.
 * @param homeDirectory - The home directory.
 * @returns The absolute path.
 */
export function expandConfigPath(path: string, configPath: string, homeDirectory: string): string {
  if (path === "~" || path.startsWith("~/")) {
    return join(homeDirectory, path.slice(1));
  }
  return isAbsolute(path) ? path : resolve(dirname(configPath), path);
}

/**
 * Returns the file's instructions: `system_prompt`, else the text of `system_prompt_file`, else `null`.
 * @param effective - The effective configuration.
 * @param configPath - The configuration file.
 * @param homeDirectory - The home directory.
 * @returns The instructions.
 * @throws {Error} When the prompt file cannot be read.
 */
export function readFileInstructions(effective: EffectiveConfig, configPath: string, homeDirectory: string): string | null {
  if (effective.system_prompt !== null) {
    return effective.system_prompt;
  }
  if (effective.system_prompt_file === null) {
    return null;
  }
  const path = expandConfigPath(effective.system_prompt_file, configPath, homeDirectory);
  try {
    return readFileSync(path, "utf8");
  } catch (error: unknown) {
    throw new Error(`system_prompt_file ${path} cannot be read: ${error instanceof Error ? error.message : String(error)}`, {
      cause: error,
    });
  }
}

/**
 * Describes a configuration error for the terminal.
 * @param error - The error.
 * @param configPath - The file it came from.
 * @returns One line.
 */
export function describeConfigError(error: ConfigError, configPath: string): string {
  return `${configPath} is invalid: ${error.code} at ${error.path === "" ? "the top level" : error.path}${
    error.keyword === undefined ? "" : ` (${error.keyword})`
  }`;
}

/** Resolves a parsed file with the harness's section, default model, and default workspace. */
function resolveHarnessConfig(value: unknown, harness: AgentHarness): EffectiveConfig {
  const workspace = harness.defaultWorkspaceLocation === "launch_directory" ? process.cwd() : defaultWorkspace;
  return resolveConfig(value, harness.configSection, harness.defaultModel, workspace);
}

function renderConfigYaml(answers: InitAnswers, harness: AgentHarness): string {
  const document: Record<string, unknown> = {};
  if (answers.model !== null) {
    document.model = answers.model;
  }
  if (answers.systemPrompt !== null) {
    document.system_prompt = answers.systemPrompt;
  }
  document.dex = { server: "auto" };
  document.connectors = { mcp: "auto", ...(answers.connectors === null ? {} : { enabled: [...answers.connectors] }) };
  if (answers.isWorkspaceWritten !== false) {
    document.workspace = answers.workspace;
  }
  document.local_tools = {
    files: answers.localTools.has("files"),
    shell: { enabled: answers.localTools.has("shell"), allow: [] },
    web_fetch: answers.localTools.has("web_fetch"),
    browser: answers.localTools.has("browser"),
  };
  document.media = { store: "auto" };
  document[harness.configSection] = {};
  const header = [
    `# Configuration of dex-agents-${harness.name}. Regenerate it with: dex-agents-${harness.name} init`,
    "# Provider API keys come from environment variables; never put secrets here.",
    "# connectors.enabled omitted: every connection authorized in Dex Web.",
    "",
  ].join("\n");
  return `${header}${stringify(document)}`;
}

function splitList(text: string): string[] {
  return text
    .split(",")
    .map((item) => item.trim())
    .filter((item) => item !== "");
}

function parseLocalToolGroups(text: string): ReadonlySet<LocalToolGroup> {
  const trimmed = text.trim();
  if (trimmed === "") {
    return new Set(localToolGroups);
  }
  if (trimmed.toLowerCase() === "none") {
    return new Set();
  }
  const items = splitList(trimmed);
  return new Set(localToolGroups.filter((group) => items.includes(group)));
}
