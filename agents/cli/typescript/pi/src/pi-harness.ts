// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { getAgentDir } from "@earendil-works/pi-coding-agent";
import type { AgentSessionFlow } from "@superdurable/dex-agents";
import { createBuiltinAgentSessionFlow, type BuiltinLoopAgentRunner } from "@superdurable/dex-agents/builtin-loop";
import { ConfigError, type AgentHarness, type HarnessContext } from "@superdurable/dex-agents-cli-core";

import { maximumBashTimeoutSeconds, PiCodingToolSource, piCodingLocalTools } from "./pi-coding-tool-source.js";
import { PiModelClients } from "./pi-model-clients.js";
import { PrefixedConnectorToolSource } from "./prefixed-connector-tool-source.js";
import { ProjectContextFiles, type ProjectContextFile } from "./project-context-files.js";

/** The `sourceName` of the CLI core's connectors tool source. */
const connectorsToolSourceName = "connectors";

/**
 * The base Flow type of the command's sessions, before the CLI core namespaces it.
 *
 * @since 0.5.0
 */
export const piCliFlowType = "DexAgentsPiCliSession";

/**
 * The model the command uses when the configuration file sets none, as `<provider>/<model>`.
 *
 * @since 0.5.0
 */
export const defaultPiModel = "anthropic/claude-opus-5-5";

/**
 * The instructions the agent gets when the configuration sets none.
 *
 * @since 0.5.0
 */
export const defaultInstructions =
  "You are a helpful coding assistant. Use your tools to read, search, and change the files in your working " +
  "directory, to run commands there, and to work with the connected services.";

/**
 * The options of the configuration file's `pi` section.
 *
 * @since 0.5.0
 */
export interface PiSection {
  /**
   * Whether each model call adds the project's context files, such as `AGENTS.md`, to the instructions, as Pi
   * does. `load_project_context` in the file; defaults to `true`.
   */
  readonly isProjectContextLoaded: boolean;
  /**
   * Base URLs that replace pi-ai's catalog URL of a provider, by provider, such as
   * `{ anthropic: "https://llm-proxy.example.com" }`. `provider_base_urls` in the file; defaults to none.
   */
  readonly providerBaseUrls: Readonly<Record<string, string>>;
  /**
   * Seconds a `bash` command runs before it is killed when the model passes no `timeout`, or `undefined` for no
   * timeout, as in Pi. `bash_timeout_seconds` in the file, a whole number from 1 to `maximumBashTimeoutSeconds`.
   * Changed in 0.6.0: defaults to `undefined` instead of 120.
   */
  readonly bashTimeoutSeconds: number | undefined;
}

/**
 * The `dex-agents-pi` command: a Pi coding agent in the terminal, whose loop Dex runs.
 *
 * Every model call is one `RunModel` Step through pi-ai, and every tool call is its own tool Step. The model ID is
 * `<provider>/<model>`, as pi-ai's catalog names them, and the API key comes only from the provider's environment
 * variable. pi-coding-agent's own `read`, `grep`, `find`, `ls`, `write`, `edit`, and `bash` replace the CLI core's
 * `files` and `shell` groups, with Pi's behavior and Pi's approval default: no approval unless `approval.always`
 * names the tool; see {@link PiCodingToolSource}. The CLI core's web fetch, browser, and connector tools stay, and
 * the agent can ask the user questions. A connector tool whose name a Pi tool has gets the `connector_` prefix, and
 * every connector write waits for approval; see {@link PrefixedConnectorToolSource}. Each model call adds the
 * workspace's `AGENTS.md` files unless `pi.load_project_context` is `false`.
 *
 * The workspace is the directory the command starts in, as in Pi, unless the file sets `workspace`.
 *
 * Changed in 0.6.0: Pi's own tool behavior and approval default, connector prefixes, and the launch directory as the
 * default workspace.
 *
 * @example
 * ```ts
 * import { processAgentCliIo, runAgentCli } from "@superdurable/dex-agents-cli-core";
 * import { piHarness } from "@superdurable/dex-agents-pi-cli";
 *
 * process.exit(await runAgentCli(piHarness, processAgentCliIo()));
 * ```
 * @since 0.5.0
 */
export const piHarness: AgentHarness = {
  name: "pi",
  configSection: "pi",
  flowType: piCliFlowType,
  defaultModel: defaultPiModel,
  replacedLocalTools: ["files", "shell"],
  defaultWorkspaceLocation: "launch_directory",
  createSessionFlow: (context) => createPiSessionFlow(context),
};

/**
 * Builds the built-in agent loop session Flow from the effective configuration.
 *
 * It builds the model client of the file's model at once, so a missing API key stops the command before the chat
 * starts. A turn whose session settings pick another model builds that model's client in its first model call.
 *
 * With `context.mediaStore`, the session takes the images the user attaches with `/image`, and the images that the
 * Pi tools return, such as `read` of an image file, reach the model too. Without it, the session refuses user images
 * and each tool image reaches the model as a text note.
 *
 * @param context - The configuration, workspace, and tool sources.
 * @param environment - The environment the API keys come from; defaults to `process.env`.
 * @returns The Flow, with `context.flowType` as its type.
 * @throws {ConfigError} When the `pi` section has an unknown key or a value of the wrong type.
 * @throws {Error} When the file's model is malformed or unknown to pi-ai, or its provider's API key is missing.
 * @since 0.5.0
 */
export function createPiSessionFlow(
  context: HarnessContext,
  environment: NodeJS.ProcessEnv = process.env,
): AgentSessionFlow<BuiltinLoopAgentRunner> {
  const section = readPiSection(context.harnessOptions);
  const modelClients = new PiModelClients(section.providerBaseUrls, environment);
  const projectContext = new ProjectContextFiles(context.workspace, getAgentDir(), (line) =>
    process.stderr.write(`${line}\n`),
  );
  return createBuiltinAgentSessionFlow(
    {
      name: "pi",
      instructions: async (turn) =>
        buildInstructions(
          context.resolveTurn(turn.sessionSettings ?? {}).instructions ?? defaultInstructions,
          section.isProjectContextLoaded ? await projectContext.load() : [],
        ),
      model: modelClients.clientFor(context.config.model),
      resolveModel: (settings) => modelClients.clientFor(context.resolveTurn(settings).model),
      toolSources: [
        ...context.toolSources.map((source) =>
          source.sourceName === connectorsToolSourceName
            ? new PrefixedConnectorToolSource(source, new Set(Object.keys(piCodingLocalTools)), context)
            : source,
        ),
        new PiCodingToolSource(context, section.bashTimeoutSeconds),
      ],
      builtInTools: ["ask_user"],
    },
    context.flowType,
    context.mediaStore === undefined ? {} : { mediaStore: context.mediaStore },
  );
}

/**
 * Reads the `pi` section of the configuration file.
 *
 * @param options - The section, `{}` when the file omits it.
 * @returns The options, with defaults filled in.
 * @throws {ConfigError} A `schema` error with the JSON Pointer of the first unknown key or wrong value.
 * @since 0.5.0
 */
export function readPiSection(options: Readonly<Record<string, unknown>>): PiSection {
  for (const key of Object.keys(options).sort()) {
    if (!piSectionKeys.includes(key)) {
      throw new ConfigError("schema", `/pi/${escapeJsonPointer(key)}`, "additionalProperties");
    }
  }
  const bashTimeoutSeconds = options["bash_timeout_seconds"];
  if (bashTimeoutSeconds !== undefined) {
    if (typeof bashTimeoutSeconds !== "number" || !Number.isSafeInteger(bashTimeoutSeconds)) {
      throw new ConfigError("schema", "/pi/bash_timeout_seconds", "type");
    }
    if (bashTimeoutSeconds < 1) {
      throw new ConfigError("schema", "/pi/bash_timeout_seconds", "minimum");
    }
    if (bashTimeoutSeconds > maximumBashTimeoutSeconds) {
      throw new ConfigError("schema", "/pi/bash_timeout_seconds", "maximum");
    }
  }
  const isProjectContextLoaded = options["load_project_context"] ?? true;
  if (typeof isProjectContextLoaded !== "boolean") {
    throw new ConfigError("schema", "/pi/load_project_context", "type");
  }
  const baseUrls = options["provider_base_urls"] ?? {};
  if (typeof baseUrls !== "object" || baseUrls === null || Array.isArray(baseUrls)) {
    throw new ConfigError("schema", "/pi/provider_base_urls", "type");
  }
  const providerBaseUrls: Record<string, string> = {};
  for (const [provider, url] of Object.entries(baseUrls as Record<string, unknown>).sort(([left], [right]) =>
    left < right ? -1 : left > right ? 1 : 0,
  )) {
    const pointer = `/pi/provider_base_urls/${escapeJsonPointer(provider)}`;
    if (typeof url !== "string") {
      throw new ConfigError("schema", pointer, "type");
    }
    if (!isHttpUrl(url)) {
      throw new ConfigError("invalid_url", pointer);
    }
    providerBaseUrls[provider] = url;
  }
  return { isProjectContextLoaded, providerBaseUrls, bashTimeoutSeconds };
}

const piSectionKeys: readonly string[] = ["bash_timeout_seconds", "load_project_context", "provider_base_urls"];

/** Appends the project context files to the instructions, in Pi's own format. */
function buildInstructions(instructions: string, contextFiles: readonly ProjectContextFile[]): string {
  if (contextFiles.length === 0) {
    return instructions;
  }
  const projectContext = [
    "Project-specific instructions and guidelines:",
    ...contextFiles.map(({ path, content }) => `<project_instructions path="${path}">\n${content}\n</project_instructions>`),
  ].join("\n\n");
  return `${instructions}\n\n${projectContext}`;
}

function isHttpUrl(text: string): boolean {
  try {
    const url = new URL(text);
    return url.protocol === "https:" || url.protocol === "http:";
  } catch {
    return false;
  }
}

function escapeJsonPointer(key: string): string {
  return key.replaceAll("~", "~0").replaceAll("/", "~1");
}
