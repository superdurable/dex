// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { randomBytes } from "node:crypto";
import {
  closeSync,
  existsSync,
  fchmodSync,
  fsyncSync,
  lstatSync,
  mkdirSync,
  openSync,
  readFileSync,
  readlinkSync,
  renameSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { basename, dirname, isAbsolute, join, resolve } from "node:path";

import { stringify } from "yaml";

import type { AgentHarness, LocalToolGroup } from "./agent-harness.js";
import { localToolGroups } from "./agent-harness.js";
import {
  ConfigError,
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
  /**
   * The enabled connector entries, such as `slack/support-slack`, or `null` for every authorized connection.
   *
   * Changed in 0.6.9: an entry may also be a map with a tools list, as written in an existing file.
   */
  readonly connectors: readonly InitConnectorEntry[] | null;
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
  /**
   * The shell commands that run without approval. Defaults to none. With an existing file, an empty list keeps the
   * file's list.
   *
   * @since 0.6.9
   */
  readonly shellAllow?: readonly string[];
}

/**
 * One `connectors.enabled` entry: a connector key, or a map from one key to its tools list.
 *
 * @since 0.6.9
 */
export type InitConnectorEntry = string | Readonly<Record<string, unknown>>;

/**
 * The configuration file `init` finds before it writes.
 *
 * `valid` carries the parsed file, which `init` keeps except for the fields it asks about. `invalid` is a file that
 * is not YAML or fails the configuration rules; `init` replaces it.
 *
 * @since 0.6.9
 */
export type ExistingConfigFile =
  | { readonly kind: "absent" }
  | { readonly kind: "valid"; readonly document: Readonly<Record<string, unknown>> }
  | { readonly kind: "invalid" };

/** Asks one question and returns the answer, or `null` when input ended. */
export type AskQuestion = (question: string) => Promise<string | null>;

// The configuration may hold Dex server settings, so a new file is private to its owner.
const defaultConfigFileMode = 0o600;

// Matches the MAXSYMLINKS limit of Linux.
const maximumSymbolicLinkHops = 40;

// init asks for the model, so an existing file without one is still kept.
const modelPlaceholderForExistingFile = "init/model-placeholder";

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
 * Reads the configuration file that `init` is about to write over.
 *
 * A file counts as valid when it is YAML and passes the configuration rules. A missing `model` does not make it
 * invalid, because `init` asks for the model.
 * @param configPath - The file.
 * @param harness - The command's harness.
 * @returns Whether the file is absent, valid with its parsed value, or invalid.
 * @throws {Error} When the file exists but cannot be read.
 * @since 0.6.9
 */
export function readExistingConfigFile(configPath: string, harness: AgentHarness): ExistingConfigFile {
  if (!existsSync(configPath)) {
    return { kind: "absent" };
  }
  const text = readFileSync(configPath, "utf8");
  try {
    const value = parseConfigYaml(text);
    resolveConfig(value, harness.configSection, harness.defaultModel ?? modelPlaceholderForExistingFile);
    return { kind: "valid", document: isRecord(value) ? value : {} };
  } catch (error: unknown) {
    if (error instanceof ConfigError) {
      return { kind: "invalid" };
    }
    throw error;
  }
}

/**
 * Copies an invalid configuration file to `NAME.yaml.bak` before `init` replaces it.
 *
 * The copy replaces an earlier `NAME.yaml.bak`. When that path is a symbolic link, the copy replaces the link
 * itself and leaves the file it pointed at unchanged.
 * @param configPath - The invalid file.
 * @returns The path of the copy.
 * @throws {Error} When the file cannot be read or the copy cannot be written.
 * @since 0.6.9
 */
export function backUpInvalidConfigFile(configPath: string): string {
  const backupPath = `${configPath}.bak`;
  try {
    replaceFileAtomically(backupPath, readFileSync(configPath), statSync(configPath).mode & 0o777);
  } catch (error: unknown) {
    throw new Error(`cannot back up ${configPath} to ${backupPath}: ${describeFileError(error)}`, { cause: error });
  }
  return backupPath;
}

/**
 * Returns the line `init` prints after it replaced an invalid configuration file.
 * @param configPath - The replaced file.
 * @param backupPath - The copy of the old file.
 * @returns One line.
 * @since 0.6.9
 */
export function describeReplacedInvalidConfigFile(configPath: string, backupPath: string): string {
  return `${configPath} was invalid, so init replaced it; the old file is at ${backupPath}.`;
}

/**
 * Returns the answers that `--yes` and non-interactive runs use: the command's default model, the harness
 * instructions, every authorized connection, the harness's default workspace, and every local tool group. For a
 * harness whose default workspace is the launch directory, the workspace is that directory and is not written.
 *
 * Changed in 0.6.9: with an existing valid file, the answers are the file's values, the same answers that pressing
 * Enter at every question gives.
 * @param harness - The command's harness.
 * @param existingDocument - The parsed existing file, or `undefined` for none.
 * @returns The default answers.
 */
export function defaultInitAnswers(
  harness: AgentHarness,
  existingDocument?: Readonly<Record<string, unknown>>,
): InitAnswers {
  if (existingDocument !== undefined) {
    return existingInitAnswers(harness, existingDocument);
  }
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
 *
 * Changed in 0.6.9: with an existing file, each default is the file's value.
 * @param harness - The command's harness.
 * @param ask - Asks one question.
 * @param log - Writes notes for the person at the terminal.
 * @param existingDocument - The parsed existing file, whose values become the defaults, or `undefined` for none.
 * @returns The answers, or `null` when input ended.
 */
export async function askInitQuestions(
  harness: AgentHarness,
  ask: AskQuestion,
  log: (line: string) => void,
  existingDocument?: Readonly<Record<string, unknown>>,
): Promise<InitAnswers | null> {
  const defaults = defaultInitAnswers(harness, existingDocument);
  log(
    existingDocument === undefined
      ? `Creating the configuration of dex-agents-${harness.name}. Press Enter to take the default in brackets.`
      : `Updating the configuration of dex-agents-${harness.name}; init keeps the fields it does not ask about. ` +
          "Press Enter to take the default in brackets.",
  );
  let model: string | null = null;
  for (;;) {
    const answer = await ask(`Model${defaults.model === null ? "" : ` [${defaults.model}]`}: `);
    if (answer === null) {
      return null;
    }
    model = answer.trim() === "" ? defaults.model : answer.trim();
    if (model !== null) {
      break;
    }
    log("This command has no default model; enter one.");
  }
  const promptDefault =
    defaults.systemPrompt !== null
      ? "the current prompt"
      : typeof existingDocument?.system_prompt_file === "string"
        ? "the current system_prompt_file"
        : "the harness default";
  const prompt = await ask(`System prompt [${promptDefault}]: `);
  if (prompt === null) {
    return null;
  }
  const connectorsDefault =
    defaults.connectors === null ? "every authorized connection" : defaults.connectors.map(connectorEntryKey).join(", ");
  const connectors = await ask(`Connectors, comma-separated, such as slack/support-slack, hackernews [${connectorsDefault}]: `);
  if (connectors === null) {
    return null;
  }
  const workspaceDefault =
    defaults.isWorkspaceWritten === false ? `the directory the command starts in, ${defaults.workspace}` : defaults.workspace;
  const workspace = await ask(`Workspace for local tools [${workspaceDefault}]: `);
  if (workspace === null) {
    return null;
  }
  const toolsDefault =
    defaults.localTools.size === 0
      ? "none"
      : localToolGroups.filter((group) => defaults.localTools.has(group)).join(", ");
  const tools = await ask(`Local tools, comma-separated [${toolsDefault}] (or "none"): `);
  if (tools === null) {
    return null;
  }
  const localTools = tools.trim() === "" ? defaults.localTools : parseLocalToolGroups(tools);
  let shellAllow = defaults.shellAllow;
  if (localTools.has("shell")) {
    const allowDefault = shellAllow === undefined || shellAllow.length === 0 ? "none" : shellAllow.join(", ");
    const allow = await ask(`Commands that run without approval, comma-separated [${allowDefault}]: `);
    if (allow === null) {
      return null;
    }
    if (allow.trim() !== "") {
      shellAllow = splitList(allow);
    }
  }
  log("Put provider API keys in environment variables, never in the configuration file.");
  return {
    ...(shellAllow === undefined ? {} : { shellAllow }),
    model,
    systemPrompt: prompt.trim() === "" ? defaults.systemPrompt : prompt.trim(),
    connectors: connectors.trim() === "" ? defaults.connectors : splitList(connectors),
    ...(workspace.trim() === ""
      ? {
          workspace: defaults.workspace,
          ...(defaults.isWorkspaceWritten === undefined ? {} : { isWorkspaceWritten: defaults.isWorkspaceWritten }),
        }
      : { workspace: workspace.trim() }),
    localTools,
  };
}

/**
 * Writes a configuration file from answers and returns its configuration.
 *
 * Changed in 0.6.9: with an existing file, it keeps every field the answers do not cover. See
 * {@link mergeInitDocument}.
 * @param configPath - The file to write; missing directories are created.
 * @param answers - The answers.
 * @param harness - The command's harness.
 * @param existingDocument - The parsed existing file to keep, or `undefined` to write only the answers.
 * @returns The configuration read back from the file.
 * @throws {ConfigError} When the answers do not make a valid file, such as an invalid connector name.
 */
export function writeConfigFile(
  configPath: string,
  answers: InitAnswers,
  harness: AgentHarness,
  existingDocument?: Readonly<Record<string, unknown>>,
): LoadedConfig {
  const text = renderConfigYaml(answers, harness, existingDocument);
  const effective = resolveHarnessConfig(parseConfigYaml(text), harness);
  try {
    const targetPath = resolveConfigFileTarget(configPath);
    mkdirSync(dirname(targetPath), { recursive: true });
    replaceFileAtomically(targetPath, text, existingFileMode(targetPath) ?? defaultConfigFileMode);
  } catch (error: unknown) {
    throw new Error(`cannot write ${configPath}: ${describeFileError(error)}`, { cause: error });
  }
  return { effective, configPath, isFromFile: true };
}

/**
 * Returns the file a configuration path names: the end of its chain of symbolic links, which may not exist yet.
 * Every link of the chain stays.
 * @throws {Error} With code `ELOOP` when the chain has more than {@link maximumSymbolicLinkHops} links.
 */
function resolveConfigFileTarget(configPath: string): string {
  let path = configPath;
  for (let hop = 0; hop <= maximumSymbolicLinkHops; hop += 1) {
    let isSymbolicLink: boolean;
    try {
      isSymbolicLink = lstatSync(path).isSymbolicLink();
    } catch (error: unknown) {
      if (isMissingFileError(error)) {
        return path;
      }
      throw error;
    }
    if (!isSymbolicLink) {
      return path;
    }
    // A relative link target is relative to the directory of that link.
    path = resolve(dirname(path), readlinkSync(path));
  }
  throw Object.assign(new Error(`too many levels of symbolic links at ${configPath}`), { code: "ELOOP" });
}

function existingFileMode(path: string): number | undefined {
  try {
    return statSync(path).mode & 0o777;
  } catch (error: unknown) {
    if (isMissingFileError(error)) {
      return undefined;
    }
    throw error;
  }
}

/**
 * Writes a temporary file beside `path`, syncs it, and renames it over `path`.
 *
 * A crash or a full disk leaves either the old file or the new one, never a truncated file. The rename replaces a
 * symbolic link at `path` rather than the file it points at.
 */
function replaceFileAtomically(path: string, content: string | Buffer, mode: number): void {
  const temporaryPath = join(dirname(path), `.${basename(path)}.${process.pid}.${randomBytes(6).toString("hex")}.tmp`);
  const descriptor = openSync(temporaryPath, "wx", mode);
  try {
    try {
      fchmodSync(descriptor, mode);
      writeFileSync(descriptor, content);
      fsyncSync(descriptor);
    } finally {
      closeSync(descriptor);
    }
    renameSync(temporaryPath, path);
  } catch (error: unknown) {
    rmSync(temporaryPath, { force: true });
    throw error;
  }
  syncDirectory(dirname(path));
}

function syncDirectory(directory: string): void {
  // Windows cannot open a directory to sync it.
  if (process.platform === "win32") {
    return;
  }
  const descriptor = openSync(directory, "r");
  try {
    fsyncSync(descriptor);
  } finally {
    closeSync(descriptor);
  }
}

function isMissingFileError(error: unknown): boolean {
  return error instanceof Error && (error as NodeJS.ErrnoException).code === "ENOENT";
}

function describeFileError(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
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
  }${error.validForms === undefined ? "" : `. Use ${error.validForms}.`}`;
}

/** Resolves a parsed file with the harness's section, default model, and default workspace. */
function resolveHarnessConfig(value: unknown, harness: AgentHarness): EffectiveConfig {
  const workspace = harness.defaultWorkspaceLocation === "launch_directory" ? process.cwd() : defaultWorkspace;
  return resolveConfig(value, harness.configSection, harness.defaultModel, workspace);
}

/**
 * Returns the file `init` writes over an existing valid file: the answers' document, with every field of the
 * existing file that `init` does not ask about.
 *
 * - `model`, `system_prompt`, and `workspace` come from the answers' document only.
 * - `system_prompt_file` stays unless the answers' document has `system_prompt`.
 * - `connectors` stays except `enabled`, which comes from the answers' document.
 * - `local_tools` comes from the answers' document, but an empty `shell.allow` keeps the existing list.
 * - Every other top-level key of the existing file replaces the answers' value whole.
 * - A top-level key the existing file lacks keeps the answers' value.
 *
 * Both CLI cores pass the cases of `agents/conformance/cli_init.json`.
 * @param existingDocument - The parsed existing file.
 * @param initDocument - The document `init` writes without an existing file.
 * @returns The merged document.
 * @since 0.6.9
 */
export function mergeInitDocument(
  existingDocument: Readonly<Record<string, unknown>>,
  initDocument: Readonly<Record<string, unknown>>,
): Record<string, unknown> {
  const merged: Record<string, unknown> = { ...initDocument };
  for (const [key, value] of Object.entries(existingDocument)) {
    switch (key) {
      case "model":
      case "system_prompt":
      case "workspace":
        break;
      case "system_prompt_file":
        if (!("system_prompt" in initDocument)) {
          merged[key] = value;
        }
        break;
      case "connectors":
        merged[key] = mergeInitConnectors(value, initDocument.connectors);
        break;
      case "local_tools":
        merged[key] = mergeInitLocalTools(value, initDocument.local_tools);
        break;
      default:
        merged[key] = value;
    }
  }
  return merged;
}

function mergeInitConnectors(existing: unknown, init: unknown): Record<string, unknown> {
  const kept: Record<string, unknown> = isRecord(existing) ? { ...existing } : {};
  delete kept.enabled;
  return isRecord(init) && "enabled" in init ? { ...kept, enabled: init.enabled } : kept;
}

function mergeInitLocalTools(existing: unknown, init: unknown): unknown {
  const existingAllow = isRecord(existing) && isRecord(existing.shell) ? existing.shell.allow : undefined;
  if (!isRecord(init) || !isRecord(init.shell) || existingAllow === undefined) {
    return init;
  }
  const initAllow = init.shell.allow;
  const isInitAllowEmpty = initAllow === undefined || (Array.isArray(initAllow) && initAllow.length === 0);
  return isInitAllowEmpty ? { ...init, shell: { ...init.shell, allow: existingAllow } } : init;
}

/** Returns the answers that keep an existing file's values, which become the defaults of the questions. */
function existingInitAnswers(harness: AgentHarness, document: Readonly<Record<string, unknown>>): InitAnswers {
  const defaults = defaultInitAnswers(harness);
  const connectors = isRecord(document.connectors) ? document.connectors : {};
  const localTools = isRecord(document.local_tools) ? document.local_tools : {};
  const shell = isRecord(localTools.shell) ? localTools.shell : {};
  return {
    model: typeof document.model === "string" ? document.model : defaults.model,
    systemPrompt: typeof document.system_prompt === "string" ? document.system_prompt : null,
    connectors: Array.isArray(connectors.enabled) ? (connectors.enabled as InitConnectorEntry[]) : null,
    ...(typeof document.workspace === "string"
      ? { workspace: document.workspace }
      : {
          workspace: defaults.workspace,
          ...(defaults.isWorkspaceWritten === undefined ? {} : { isWorkspaceWritten: defaults.isWorkspaceWritten }),
        }),
    localTools: new Set(
      localToolGroups.filter((group) => (group === "shell" ? shell.enabled : localTools[group]) !== false),
    ),
    ...(Array.isArray(shell.allow) ? { shellAllow: shell.allow.filter((command) => typeof command === "string") } : {}),
  };
}

function connectorEntryKey(entry: InitConnectorEntry): string {
  return typeof entry === "string" ? entry : (Object.keys(entry)[0] ?? "");
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function renderConfigYaml(
  answers: InitAnswers,
  harness: AgentHarness,
  existingDocument?: Readonly<Record<string, unknown>>,
): string {
  const initDocument = initConfigDocument(answers, harness);
  const document = existingDocument === undefined ? initDocument : mergeInitDocument(existingDocument, initDocument);
  const header = [
    `# Configuration of dex-agents-${harness.name}. Regenerate it with: dex-agents-${harness.name} init`,
    "# Provider API keys come from environment variables; never put secrets here.",
    "# connectors.enabled omitted: every connection authorized in Dex Web.",
    "",
  ].join("\n");
  return `${header}${stringify(document)}`;
}

/** Returns the document of the answers alone, which `init` writes when no file exists. */
function initConfigDocument(answers: InitAnswers, harness: AgentHarness): Record<string, unknown> {
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
    shell: { enabled: answers.localTools.has("shell"), allow: [...(answers.shellAllow ?? [])] },
    web_fetch: answers.localTools.has("web_fetch"),
    browser: answers.localTools.has("browser"),
  };
  document.media = { store: "auto" };
  document.sessions = { summary: "auto" };
  document[harness.configSection] = {};
  return document;
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
