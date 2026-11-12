// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { describe, test } from "node:test";
import { fileURLToPath } from "node:url";

import { Ajv2020, type ErrorObject } from "ajv/dist/2020.js";

import {
  ConfigError,
  canonicalOperationName,
  compareCodePoints,
  isShellCommandAllowed,
  parseConfigYaml,
  readSchema,
  resolveConfig,
  resolveTurn,
} from "../../src/index.js";

const sharedCliDirectory = fileURLToPath(new URL("../../../../../", import.meta.url));
const fixture = JSON.parse(
  readFileSync(join(sharedCliDirectory, "..", "conformance", "cli_config.json"), "utf8"),
) as ConformanceFixture;

interface ExpectedError {
  readonly code: string;
  readonly path: string;
  readonly keyword?: string;
}

interface ConformanceFixture {
  readonly yaml_cases: readonly { name: string; text: string; value?: unknown; error?: ExpectedError }[];
  readonly operation_names: readonly { name: string; canonical: string }[];
  readonly shell_allow: readonly { command: string; allow: string[]; is_allowed: boolean }[];
  readonly config_cases: readonly {
    name: string;
    harness_section: string;
    harness_default_model: string | null;
    config: unknown;
    effective?: unknown;
    error?: ExpectedError;
  }[];
  readonly session_configs: Readonly<Record<string, unknown>>;
  readonly session_harness: { harness_section: string; harness_default_model: string | null };
  readonly session_cases: readonly {
    name: string;
    config: string;
    session_settings: unknown;
    turn?: unknown;
    warnings?: string[];
    error?: ExpectedError;
  }[];
}

/** Every validator error, nested anyOf branches included, as the conformance contract counts them. */
const allErrorsAjv = new Ajv2020({ strict: true, allErrors: true });
const allErrorsConfig = allErrorsAjv.compile(readSchema("config.schema.json"));
const allErrorsSessionSettings = allErrorsAjv.compile(readSchema("session-settings.schema.json"));

function schemaErrors(errors: readonly ErrorObject[] | null | undefined): Set<string> {
  return new Set((errors ?? []).map((error) => `${error.instancePath} ${error.keyword}`));
}

function expectError(resolve: () => unknown, expected: ExpectedError, validate: typeof allErrorsConfig, value: unknown) {
  const isValid = validate(value ?? {});
  if (expected.code === "schema") {
    assert.equal(isValid, false);
    assert.ok(
      schemaErrors(validate.errors).has(`${expected.path} ${expected.keyword}`),
      `validator errors ${[...schemaErrors(validate.errors)].join(", ")}`,
    );
  } else {
    assert.equal(isValid, true, "a non-schema error case must pass the schema");
  }
  assert.throws(resolve, (error: unknown) => {
    assert.ok(error instanceof ConfigError, String(error));
    assert.deepEqual(
      { code: error.code, path: error.path, ...(error.keyword === undefined ? {} : { keyword: error.keyword }) },
      expected.code === "schema" ? expected : { code: expected.code, path: expected.path },
    );
    return true;
  });
}

describe("CLI configuration conformance", () => {
  for (const testCase of fixture.yaml_cases) {
    test(`yaml: ${testCase.name}`, () => {
      if (testCase.error !== undefined) {
        expectError(() => parseConfigYaml(testCase.text), testCase.error, allErrorsConfig, {});
        return;
      }
      assert.deepEqual(parseConfigYaml(testCase.text), testCase.value);
    });
  }

  test("yaml: two documents are an error", () => {
    assert.throws(() => parseConfigYaml("model: a\n---\nmodel: b\n"), ConfigError);
  });

  for (const testCase of fixture.operation_names) {
    test(`operation name: ${testCase.name}`, () => {
      assert.equal(canonicalOperationName(testCase.name), testCase.canonical);
    });
  }

  for (const testCase of fixture.shell_allow) {
    test(`shell allow: ${JSON.stringify(testCase.command)}`, () => {
      assert.equal(isShellCommandAllowed(testCase.command, testCase.allow), testCase.is_allowed);
    });
  }

  for (const testCase of fixture.config_cases) {
    test(`config: ${testCase.name}`, () => {
      const resolve = () => resolveConfig(testCase.config, testCase.harness_section, testCase.harness_default_model);
      if (testCase.error !== undefined) {
        expectError(resolve, testCase.error, allErrorsConfig, testCase.config);
        return;
      }
      assert.deepEqual(resolve(), testCase.effective);
    });
  }

  for (const testCase of fixture.session_cases) {
    test(`session: ${testCase.name}`, () => {
      const harness = fixture.session_harness;
      const effective = resolveConfig(
        fixture.session_configs[testCase.config],
        harness.harness_section,
        harness.harness_default_model,
      );
      const resolve = () => resolveTurn(effective, testCase.session_settings);
      if (testCase.error !== undefined) {
        expectError(resolve, testCase.error, allErrorsSessionSettings, testCase.session_settings);
        return;
      }
      const { turn, warnings } = resolve();
      assert.deepEqual(turn, testCase.turn);
      assert.deepEqual(warnings, testCase.warnings);
    });
  }

  test("the example configuration files resolve", () => {
    const examplesDirectory = join(sharedCliDirectory, "examples");
    for (const name of readdirSync(examplesDirectory).filter((file) => file.endsWith(".yaml"))) {
      const effective = resolveConfig(parseConfigYaml(readFileSync(join(examplesDirectory, name), "utf8")), "claude", null);
      assert.equal(typeof effective.model, "string");
    }
  });

  test("every anyOf of the schemas names its valid forms in its title", () => {
    const untitled = (schema: unknown, pointer: string): string[] => {
      if (Array.isArray(schema)) {
        return schema.flatMap((value, index) => untitled(value, `${pointer}/${index}`));
      }
      if (typeof schema !== "object" || schema === null) {
        return [];
      }
      const object = schema as Readonly<Record<string, unknown>>;
      const own = "anyOf" in object && typeof object["title"] !== "string" ? [pointer] : [];
      return [...own, ...Object.entries(object).flatMap(([key, value]) => untitled(value, `${pointer}/${key}`))];
    };
    for (const name of ["config.schema.json", "session-settings.schema.json"]) {
      assert.deepEqual(untitled(readSchema(name), ""), [], name);
    }
  });

  test("an anyOf error ends with the failed schema's title", () => {
    assert.throws(
      () => resolveConfig({ media: { store: "/tmp/media" } }, "claude", null),
      (error: unknown) => {
        assert.ok(error instanceof ConfigError);
        assert.match(error.validForms ?? "", /^"auto" \(a directory on this machine\)/);
        assert.equal(
          error.message,
          `schema at "/media/store" (schema keyword anyOf). Use ${error.validForms ?? ""}.`,
        );
        return true;
      },
    );
  });

  test("code point order differs from UTF-16 order above the BMP", () => {
    const keys = ["\u{10000}", "\u{E000}"];
    assert.deepEqual([...keys].sort(compareCodePoints), ["\u{E000}", "\u{10000}"]);
    assert.deepEqual([...keys].sort(), ["\u{10000}", "\u{E000}"]);
  });
});
