// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Ajv2020, type ValidateFunction } from "ajv/dist/2020.js";

import { readSchema } from "../cli-config.js";

/** One terminal UI contract schema, as `dist/schemas` ships it. */
export type TerminalUiSchemaName = "launch" | "describe";

const channelSchemaId = "terminal-ui-channel";
const compiled = new Map<string, ValidateFunction>();
let channelAjv: Ajv2020 | undefined;

/**
 * Returns the validator of the launch document's or the `--describe` output's JSON Schema (2020-12), compiled once.
 *
 * The schemas are normative and copied next to the build by `scripts/copy-schemas.mjs`.
 */
export function terminalUiSchemaValidator(name: TerminalUiSchemaName): ValidateFunction {
  let validator = compiled.get(name);
  if (validator === undefined) {
    validator = new Ajv2020({ strict: true, allErrors: true }).compile(readSchema(`terminal-ui-${name}.schema.json`));
    compiled.set(name, validator);
  }
  return validator;
}

/**
 * Returns the validator of one definition of the channel schema, such as `from_tui` for every message the terminal
 * UI may send, or `put_image_result` for a method's result.
 * @param definition - The name under `$defs`.
 */
export function terminalUiChannelValidator(definition: string): ValidateFunction {
  const key = `channel#${definition}`;
  let validator = compiled.get(key);
  if (validator === undefined) {
    if (channelAjv === undefined) {
      channelAjv = new Ajv2020({ strict: true, allErrors: true });
      channelAjv.addSchema(readSchema("terminal-ui-channel.schema.json"), channelSchemaId);
    }
    validator = channelAjv.getSchema(`${channelSchemaId}#/$defs/${definition}`);
    if (validator === undefined) {
      throw new RangeError(`the channel schema has no definition ${definition}`);
    }
    compiled.set(key, validator);
  }
  return validator;
}
