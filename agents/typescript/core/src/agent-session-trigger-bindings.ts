// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { existsSync, readFileSync } from "node:fs";
import path from "node:path";

/**
 * One Connector Trigger binding that a session graph declares, before its package manifest is read.
 *
 * A hosting platform reads the bindings of a Flow Definition Graph to offer their configuration. The session Flow has
 * no application code that declares bindings, so you name them here instead. Pass the spec to
 * {@link resolveSessionTriggerBinding}, or write it as one entry of the file that `dex-agents-session-graph
 * --trigger-bindings` reads. The JSON entry has the same field names.
 *
 * The binding only declares that the Trigger delivers into the session Flow. Your Trigger target still does the
 * delivery: `newDexRPCTriggerTarget` with `rpcName: "send_user_message"` for user messages.
 *
 * @example
 * ```ts
 * const spec: SessionTriggerBindingSpec = {
 *   connectionName: "slack-workspace",
 *   packageName: "@superdurable/connector-slack",
 *   packageVersion: "0.6.0",
 *   triggerName: "threadReplyCreated",
 *   bindingName: "support-thread-replies",
 * };
 * ```
 *
 * @since 0.6.12
 */
export interface SessionTriggerBindingSpec {
  /** The Connector connection name that the hosting platform configures, such as `slack-workspace`. */
  readonly connectionName: string;
  /** The published Connector package name, such as `@superdurable/connector-slack`. */
  readonly packageName: string;
  /** The exact package version. It must equal `package.version` of the package's `dex-connector.json`. */
  readonly packageVersion: string;
  /** The Trigger name in the package manifest, such as `threadReplyCreated`. */
  readonly triggerName: string;
  /** The binding name, unique for this Connector, connection, and Trigger. */
  readonly bindingName: string;
  /**
   * The directory that holds the package's `dex-connector.json`. When omitted, the package is looked up in
   * `node_modules` directories from the search directory upward.
   */
  readonly packageDirectory?: string | undefined;
  /**
   * The configuration UI of the binding. When omitted, the binding uses the Trigger's `configurationUI` from the
   * package manifest, or no units when the manifest has none. This is the same rule as a `configurationUI` field in
   * application code that declares a binding.
   */
  readonly configurationUI?: SessionConnectorConfigurationUI | undefined;
}

/**
 * The configuration UI of one Connector Trigger binding: the UI units a hosting platform shows for it.
 *
 * @since 0.6.12
 */
export interface SessionConnectorConfigurationUI {
  /** The units, in display order. Unit IDs are unique. */
  readonly units: readonly SessionConnectorUIUnit[];
}

/**
 * One Connector UI unit of a binding's configuration UI.
 *
 * @since 0.6.12
 */
export interface SessionConnectorUIUnit {
  /** The unit instance ID, unique in the binding. */
  readonly id: string;
  /** The Connector UI unit, such as `channelPicker`. */
  readonly unitId: string;
  /** The label a hosting platform shows. It must not be blank. */
  readonly label: string;
  /** An optional description. An empty description is left out of the graph. */
  readonly description?: string | undefined;
  /** Whether the configuration needs this unit. Defaults to `false`. */
  readonly required?: boolean | undefined;
  /** Where each unit port writes into the binding configuration. Defaults to none. */
  readonly bindings?: readonly SessionConnectorUIBinding[] | undefined;
}

/**
 * Maps one UI unit port to a JSON pointer in the binding configuration.
 *
 * @since 0.6.12
 */
export interface SessionConnectorUIBinding {
  /** The unit port, such as `channelId`. Ports are unique in a unit. */
  readonly port: string;
  /** The RFC 6901 JSON pointer that the port's value is written to, such as `/channelId`. */
  readonly jsonPointer: string;
}

/**
 * One `v2.connectorTriggerBindings` entry of a Flow Definition Graph 2.0, as `dexcli visualize` writes it.
 *
 * {@link resolveSessionTriggerBinding} returns it from a {@link SessionTriggerBindingSpec}. Pass it to
 * `renderSessionFlowGraph` in `connectorTriggerBindings`.
 *
 * @since 0.6.12
 */
export interface SessionConnectorTriggerBinding {
  /** The Connector ID from the package manifest. */
  readonly connectorId: string;
  /** The Trigger name. */
  readonly triggerName: string;
  /** The Connector connection name. */
  readonly connectionName: string;
  /** The binding name. */
  readonly bindingName: string;
  /** Always empty: a Go module identifies Go bindings only. */
  readonly modulePath: string;
  /** Always empty: a Go module identifies Go bindings only. */
  readonly moduleVersion: string;
  /** The Connector release tag from the package manifest, such as `connectors/slack/go/v1.7.0`. */
  readonly releaseTag: string;
  /** The Connector package from the package manifest. */
  readonly package: { readonly ecosystem: string; readonly name: string; readonly version: string };
  /** True when the connection name and binding name are set, which a resolved binding always has. */
  readonly configurationEnabled: boolean;
  /** The configuration UI, with every unit's `required` and `bindings` filled in. */
  readonly configurationUI: {
    readonly units: readonly (SessionConnectorUIUnit & {
      readonly required: boolean;
      readonly bindings: readonly SessionConnectorUIBinding[];
    })[];
  };
}

const connectorIdPattern = /^[a-z][a-z0-9-]{1,62}$/;
const releaseTagPattern = /^connectors\/[a-z0-9][a-z0-9-]*(?:\/[a-z0-9][a-z0-9-]*)*\/v[0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?$/;
const packageVersionPattern = /^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+.a-zA-Z0-9]*)$/;
const jsonPointerPattern = /^(?:\/(?:[^~/]|~[01])*)*$/;
const maxListItems = 256;
const specFieldNames = new Set([
  "connectionName",
  "packageName",
  "packageVersion",
  "triggerName",
  "bindingName",
  "packageDirectory",
  "configurationUI",
]);
const unitFieldNames = new Set(["id", "unitId", "label", "description", "required", "bindings"]);

/**
 * Reads a Connector package manifest and returns the session graph entry of one Trigger binding.
 *
 * The entry has the same fields, order, and values as the entry that `dexcli visualize` derives from application code
 * that declares the same binding. Its configuration UI is the spec's, or else the Trigger's from the manifest.
 *
 * @example
 * ```ts
 * import { writeFileSync } from "node:fs";
 * import { renderSessionFlowGraph, resolveSessionTriggerBinding } from "@superdurable/dex-agents";
 *
 * const binding = resolveSessionTriggerBinding({
 *   connectionName: "slack-workspace",
 *   packageName: "@superdurable/connector-slack",
 *   packageVersion: "0.6.0",
 *   triggerName: "threadReplyCreated",
 *   bindingName: "support-thread-replies",
 * });
 * writeFileSync(
 *   "definitions/support-agent.json",
 *   renderSessionFlowGraph({
 *     flowType: "SupportAgent",
 *     actionPermission: "support-agent.operate",
 *     connectorTriggerBindings: [binding],
 *   }),
 * );
 * ```
 *
 * @param spec - The binding to resolve.
 * @param searchDirectory - Where a relative `packageDirectory` starts, and where the `node_modules` lookup starts.
 *   Defaults to the current working directory.
 * @returns The `v2.connectorTriggerBindings` entry.
 * @throws {Error} When the package is not installed, has no `dex-connector.json`, has another name or version, has
 *   no Trigger named `triggerName`, or when a spec field or the configuration UI is invalid.
 * @since 0.6.12
 */
export function resolveSessionTriggerBinding(
  spec: SessionTriggerBindingSpec,
  searchDirectory: string = process.cwd(),
): SessionConnectorTriggerBinding {
  const label = `Trigger binding ${spec.bindingName}`;
  for (const field of ["connectionName", "packageName", "packageVersion", "triggerName", "bindingName"] as const) {
    if (typeof spec[field] !== "string" || spec[field].trim() === "") {
      throw new Error(`${label}: ${field} must be a non-empty string`);
    }
  }
  const manifestPath = connectorManifestPath(spec, searchDirectory);
  let manifest: unknown;
  try {
    manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  } catch (error) {
    throw new Error(`${label}: cannot read Connector manifest ${manifestPath}: ${(error as Error).message}`);
  }
  const identity = connectorManifestIdentity(manifest, manifestPath);
  if (identity.package.name !== spec.packageName || identity.package.version !== spec.packageVersion) {
    throw new Error(
      `${label}: Connector manifest ${manifestPath} is for ${identity.package.name}@${identity.package.version}, not ${spec.packageName}@${spec.packageVersion}`,
    );
  }
  const factory = identity.triggers.find((candidate) => candidate.triggerName === spec.triggerName);
  if (factory === undefined) {
    const known = identity.triggers.map((candidate) => candidate.triggerName).join(", ") || "none";
    throw new Error(
      `${label}: ${spec.packageName}@${spec.packageVersion} has no Trigger ${spec.triggerName}; its Triggers: ${known}`,
    );
  }
  return canonicalSessionTriggerBinding({
    connectorId: identity.connectorId,
    triggerName: spec.triggerName,
    connectionName: spec.connectionName,
    bindingName: spec.bindingName,
    modulePath: "",
    moduleVersion: "",
    releaseTag: identity.releaseTag,
    package: identity.package,
    configurationEnabled: true,
    configurationUI: canonicalConfigurationUI(spec.configurationUI ?? factory.configurationUI ?? { units: [] }, label),
  });
}

/**
 * Parses one `--trigger-binding` value: `<connectionName>=<packageName>@<packageVersion>:<triggerName>:<bindingName>`.
 *
 * @param text - The flag value, such as `slack-workspace=@superdurable/connector-slack@0.6.0:threadReplyCreated:replies`.
 * @returns The spec, without a package directory or configuration UI.
 * @throws {Error} When the value does not have that shape.
 */
export function parseSessionTriggerBindingFlag(text: string): SessionTriggerBindingSpec {
  const shape = "<connectionName>=<packageName>@<packageVersion>:<triggerName>:<bindingName>";
  const separator = text.indexOf("=");
  const parts = text.slice(separator + 1).split(":");
  const packageText = parts.slice(0, -2).join(":");
  const versionSeparator = packageText.lastIndexOf("@");
  const spec = {
    connectionName: text.slice(0, Math.max(separator, 0)),
    packageName: packageText.slice(0, Math.max(versionSeparator, 0)),
    packageVersion: packageText.slice(versionSeparator + 1),
    triggerName: parts.at(-2) ?? "",
    bindingName: parts.at(-1) ?? "",
  };
  if (separator <= 0 || parts.length < 3 || versionSeparator <= 0 || Object.values(spec).some((value) => value === "")) {
    throw new Error(`--trigger-binding ${JSON.stringify(text)} must be ${shape}`);
  }
  return spec;
}

/**
 * Reads a `--trigger-bindings` file: a JSON array of {@link SessionTriggerBindingSpec} objects.
 *
 * @param filePath - The file to read. A relative `packageDirectory` in it is relative to the file's directory.
 * @returns The specs, in file order, each with an absolute `packageDirectory` when it has one.
 * @throws {Error} When the file cannot be read, is not a JSON array of objects, or has an unknown field.
 */
export function readSessionTriggerBindingsFile(filePath: string): SessionTriggerBindingSpec[] {
  let entries: unknown;
  try {
    entries = JSON.parse(readFileSync(filePath, "utf8"));
  } catch (error) {
    throw new Error(`cannot read Trigger bindings file ${filePath}: ${(error as Error).message}`);
  }
  if (!Array.isArray(entries)) {
    throw new Error(`Trigger bindings file ${filePath} must contain a JSON array`);
  }
  return entries.map((entry: unknown, index) => {
    if (!isPlainObject(entry)) {
      throw new Error(`Trigger bindings file ${filePath}: entry ${index} must be an object`);
    }
    const unknown = Object.keys(entry).filter((name) => !specFieldNames.has(name));
    if (unknown.length > 0) {
      throw new Error(`Trigger bindings file ${filePath}: entry ${index} has unknown fields ${unknown.join(", ")}`);
    }
    const spec = entry as unknown as SessionTriggerBindingSpec;
    if (spec.packageDirectory === undefined) {
      return spec;
    }
    if (typeof spec.packageDirectory !== "string") {
      throw new Error(`Trigger bindings file ${filePath}: entry ${index} packageDirectory must be a string`);
    }
    return { ...spec, packageDirectory: path.resolve(path.dirname(filePath), spec.packageDirectory) };
  });
}

/**
 * Returns the binding with its fields in graph order, and checks it.
 *
 * @throws {TypeError} When a field is missing or the configuration UI is invalid.
 */
export function canonicalSessionTriggerBinding(binding: SessionConnectorTriggerBinding): SessionConnectorTriggerBinding {
  const label = `Trigger binding ${String(binding.bindingName)}`;
  for (const field of ["connectorId", "triggerName", "connectionName", "bindingName", "releaseTag"] as const) {
    if (typeof binding[field] !== "string" || binding[field] === "") {
      throw new TypeError(`${label}: ${field} must be a non-empty string`);
    }
  }
  const identity = binding.package;
  if (!isPlainObject(identity) || [identity.ecosystem, identity.name, identity.version].some((value) => typeof value !== "string")) {
    throw new TypeError(`${label}: package must have ecosystem, name, and version`);
  }
  return {
    connectorId: binding.connectorId,
    triggerName: binding.triggerName,
    connectionName: binding.connectionName,
    bindingName: binding.bindingName,
    modulePath: "",
    moduleVersion: "",
    releaseTag: binding.releaseTag,
    package: { ecosystem: identity.ecosystem, name: identity.name, version: identity.version },
    configurationEnabled: true,
    configurationUI: canonicalConfigurationUI(binding.configurationUI, label),
  };
}

function connectorManifestPath(spec: SessionTriggerBindingSpec, searchDirectory: string): string {
  if (spec.packageDirectory !== undefined) {
    const manifestPath = path.join(path.resolve(searchDirectory, spec.packageDirectory), "dex-connector.json");
    if (!existsSync(manifestPath)) {
      throw new Error(`Trigger binding ${spec.bindingName}: package ${spec.packageName} has no Connector manifest at ${manifestPath}`);
    }
    return manifestPath;
  }
  const start = path.resolve(searchDirectory);
  for (let directory = start; ; directory = path.dirname(directory)) {
    const packageDirectory = path.join(directory, "node_modules", spec.packageName);
    if (existsSync(path.join(packageDirectory, "package.json"))) {
      const manifestPath = path.join(packageDirectory, "dex-connector.json");
      if (!existsSync(manifestPath)) {
        throw new Error(`Trigger binding ${spec.bindingName}: package ${spec.packageName} has no Connector manifest at ${manifestPath}`);
      }
      return manifestPath;
    }
    if (path.dirname(directory) === directory) {
      throw new Error(`Trigger binding ${spec.bindingName}: package ${spec.packageName} is not installed in node_modules above ${start}`);
    }
  }
}

interface ManifestTrigger {
  readonly triggerName: string;
  readonly configurationUI: SessionConnectorConfigurationUI | undefined;
}

function connectorManifestIdentity(
  manifest: unknown,
  manifestPath: string,
): {
  connectorId: string;
  releaseTag: string;
  package: { ecosystem: string; name: string; version: string };
  triggers: ManifestTrigger[];
} {
  const invalid = (reason: string): Error => new Error(`Connector manifest ${manifestPath}: ${reason}`);
  if (!isPlainObject(manifest)) {
    throw invalid("must be a JSON object");
  }
  const { connectorId, releaseTag, factories } = manifest;
  const identity = manifest["package"];
  if (typeof connectorId !== "string" || !connectorIdPattern.test(connectorId)) {
    throw invalid(`connectorId ${JSON.stringify(connectorId)} must be lowercase kebab-case`);
  }
  if (typeof releaseTag !== "string" || !releaseTagPattern.test(releaseTag)) {
    throw invalid(`releaseTag ${JSON.stringify(releaseTag)} must be an exact tag such as connectors/<name>/v1.2.3`);
  }
  if (
    !isPlainObject(identity) ||
    (identity.ecosystem !== "npm" && identity.ecosystem !== "pypi") ||
    typeof identity.name !== "string" ||
    identity.name.trim() === "" ||
    typeof identity.version !== "string" ||
    !packageVersionPattern.test(identity.version)
  ) {
    throw invalid("package must have an npm or pypi ecosystem, a name, and an exact version");
  }
  if (!Array.isArray(factories)) {
    throw invalid("factories must be an array");
  }
  const triggers: ManifestTrigger[] = [];
  for (const factory of factories as unknown[]) {
    if (!isPlainObject(factory) || factory.kind !== "trigger") {
      continue;
    }
    if (typeof factory.triggerName !== "string" || factory.triggerName === "") {
      throw invalid(`Trigger factory ${String(factory.name)} requires triggerName`);
    }
    const configurationUI = factory.configurationUI;
    if (configurationUI !== undefined && (!isPlainObject(configurationUI) || !Array.isArray(configurationUI.units))) {
      throw invalid(`Trigger factory ${String(factory.name)}: configurationUI requires units`);
    }
    for (const unit of (configurationUI?.units ?? []) as unknown[]) {
      if (!isPlainObject(unit) || typeof unit.required !== "boolean" || !Array.isArray(unit.bindings)) {
        throw invalid(`Trigger factory ${String(factory.name)}: configurationUI units require required and bindings`);
      }
    }
    triggers.push({
      triggerName: factory.triggerName,
      configurationUI: configurationUI as SessionConnectorConfigurationUI | undefined,
    });
  }
  return {
    connectorId,
    releaseTag,
    package: { ecosystem: identity.ecosystem, name: identity.name, version: identity.version },
    triggers,
  };
}

function canonicalConfigurationUI(
  configurationUI: SessionConnectorConfigurationUI,
  label: string,
): SessionConnectorTriggerBinding["configurationUI"] {
  const invalid = (reason: string): TypeError => new TypeError(`${label}: configurationUI ${reason}`);
  if (!isPlainObject(configurationUI) || !Array.isArray(configurationUI.units)) {
    throw invalid("must be an object with a units array");
  }
  if (configurationUI.units.length > maxListItems) {
    throw invalid(`has more than ${maxListItems} units`);
  }
  const unitIds = new Set<string>();
  const units = configurationUI.units.map((unit: unknown) => {
    if (!isPlainObject(unit) || Object.keys(unit).some((name) => !unitFieldNames.has(name))) {
      throw invalid(`units take only ${[...unitFieldNames].join(", ")}`);
    }
    const { id, unitId, description } = unit;
    const label = unit["label"];
    const required = unit.required ?? false;
    const bindings = unit.bindings ?? [];
    if (
      typeof id !== "string" ||
      id === "" ||
      unitIds.has(id) ||
      typeof unitId !== "string" ||
      unitId === "" ||
      typeof label !== "string" ||
      label.trim() === ""
    ) {
      throw invalid("units require unique IDs, unitId, and label");
    }
    unitIds.add(id);
    if ((description !== undefined && typeof description !== "string") || typeof required !== "boolean") {
      throw invalid(`unit ${id} needs a string description and a boolean required`);
    }
    if (!Array.isArray(bindings) || bindings.length > maxListItems) {
      throw invalid(`unit ${id} bindings must be an array of at most ${maxListItems} items`);
    }
    const ports = new Set<string>();
    const canonicalBindings = bindings.map((binding: unknown) => {
      if (!isPlainObject(binding) || typeof binding.port !== "string" || binding.port === "" || ports.has(binding.port)) {
        throw invalid(`unit ${id} bindings require unique non-empty ports`);
      }
      ports.add(binding.port);
      if (typeof binding.jsonPointer !== "string" || !jsonPointerPattern.test(binding.jsonPointer)) {
        throw invalid(`unit ${id} binding ${binding.port} has invalid JSON pointer ${JSON.stringify(binding.jsonPointer)}`);
      }
      return { port: binding.port, jsonPointer: binding.jsonPointer };
    });
    return {
      id,
      unitId,
      label,
      ...(description === undefined || description === "" ? {} : { description }),
      required,
      bindings: canonicalBindings,
    };
  });
  return { units };
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
