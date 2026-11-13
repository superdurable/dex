// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Runs the launcher's own code over every case file of terminal UI contract 1 in agents/conformance/terminal_ui.

import assert from "node:assert/strict";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, test } from "node:test";
import { fileURLToPath } from "node:url";

import type { DexAuthSettings } from "../../src/dex-connection.js";
import {
  chatLaunchDocument,
  maxLaunchNoticeBytes,
  noticeText,
  terminalUiAuthOf,
} from "../../src/terminal-ui/launch-document.js";
import { LocalRequestRelay, refusal } from "../../src/terminal-ui/local-request-relay.js";
import { isOpenUrlAllowed } from "../../src/terminal-ui/open-url-policy.js";
import { candidateSessionIds, idempotencyKeyTimeMs } from "../../src/terminal-ui/request-keys.js";
import {
  RequestOutcomeTable,
  type LocalRequestOutcome,
  type RequestOutcomeEntry,
} from "../../src/terminal-ui/request-outcome-table.js";
import type { ChannelMessage } from "../../src/terminal-ui/terminal-ui-channel.js";
import {
  buildTerminalUiEnvironment,
  terminalUiAuthVariables,
  type TerminalUiAuth,
} from "../../src/terminal-ui/terminal-ui-environment.js";
import { chooseContract, launcherContractVersions, parseDescription } from "../../src/terminal-ui/terminal-ui-negotiation.js";
import { terminalUiChannelValidator, terminalUiSchemaValidator } from "../../src/terminal-ui/terminal-ui-schemas.js";
import { isTerminalUiSelected } from "../../src/terminal-ui/terminal-ui-selection.js";
import { stopRecordedGroups } from "../../src/worker-host/leftover-worker-hosts.js";
import type { ProcessIdentity } from "../../src/worker-host/process-identity.js";
import { startSessionForKey, type SessionStarter } from "../../src/worker-host/worker-host-methods.js";

const caseDirectory = fileURLToPath(new URL("../../../../../../conformance/terminal_ui/", import.meta.url));

function readCases<T>(name: string): T {
  return JSON.parse(readFileSync(join(caseDirectory, name), "utf8")) as T;
}

describe("terminal UI contract 1: session_ids.json", () => {
  interface SessionIdCases {
    readonly vectors: readonly { harness: string; idempotency_key: string; session_id: string; long_session_id: string }[];
    readonly invalid_keys: readonly { idempotency_key: string; description: string }[];
    readonly collisions: readonly { harness: string; idempotency_keys: string[]; session_id: string; long_session_ids: string[] }[];
    readonly start_session_cases: readonly {
      name: string;
      harness: string;
      idempotency_key: string;
      started_request_ids: Readonly<Record<string, string>>;
      expected: { session_id?: string; error?: string };
    }[];
    readonly start_session_sequences: readonly {
      name: string;
      harness: string;
      steps: {
        op: "start" | "purge";
        idempotency_key?: string;
        session_id?: string;
        expected?: { session_id?: string; error?: string };
        starts_new_run?: boolean;
      }[];
    }[];
  }
  const cases = readCases<SessionIdCases>("session_ids.json");

  test("each key derives its short and long session IDs", () => {
    for (const vector of cases.vectors) {
      assert.deepEqual(candidateSessionIds(vector.harness, vector.idempotency_key), [vector.session_id, vector.long_session_id]);
    }
  });

  test("invalid keys are refused", () => {
    for (const invalid of cases.invalid_keys) {
      assert.equal(idempotencyKeyTimeMs(invalid.idempotency_key), undefined, invalid.description);
      assert.throws(() => candidateSessionIds("openai-sdk", invalid.idempotency_key), RangeError, invalid.description);
    }
  });

  test("colliding keys share the short ID and differ in the long one", () => {
    for (const collision of cases.collisions) {
      const derived = collision.idempotency_keys.map((key) => candidateSessionIds(collision.harness, key));
      assert.deepEqual(derived.map(([short]) => short), collision.idempotency_keys.map(() => collision.session_id));
      assert.deepEqual(derived.map(([, long]) => long), collision.long_session_ids);
    }
  });

  /** A model of the Dex server: each retained Flow ID and the start request ID that started it. */
  function modelStarter(retained: Map<string, string>, idempotencyKey: string, newRuns: string[]): SessionStarter {
    return {
      isStarted: (sessionId) => Promise.resolve(retained.has(sessionId)),
      start: (sessionId) => {
        const startedBy = retained.get(sessionId);
        if (startedBy === undefined) {
          retained.set(sessionId, idempotencyKey);
          newRuns.push(sessionId);
          return Promise.resolve("started");
        }
        return Promise.resolve(startedBy === idempotencyKey ? "started" : "taken");
      },
    };
  }

  function outcomeOf(outcome: LocalRequestOutcome): { session_id?: string; error?: string } {
    return "error" in outcome ? { error: outcome.error.code } : { session_id: String(outcome.result.session_id) };
  }

  test("start_session ends with the case's session or id_conflict", async () => {
    for (const startCase of cases.start_session_cases) {
      const retained = new Map(Object.entries(startCase.started_request_ids));
      const outcome = await startSessionForKey(
        startCase.harness,
        startCase.idempotency_key,
        modelStarter(retained, startCase.idempotency_key, []),
      );
      assert.deepEqual(outcomeOf(outcome), startCase.expected, startCase.name);
    }
  });

  test("start_session sequences with purges end as the cases say", async () => {
    for (const sequence of cases.start_session_sequences) {
      const retained = new Map<string, string>();
      for (const step of sequence.steps) {
        if (step.op === "purge") {
          retained.delete(step.session_id ?? "");
          continue;
        }
        const newRuns: string[] = [];
        const key = step.idempotency_key ?? "";
        const outcome = await startSessionForKey(sequence.harness, key, modelStarter(retained, key, newRuns));
        assert.deepEqual(outcomeOf(outcome), step.expected, `${sequence.name}: ${key}`);
        assert.equal(newRuns.length > 0, step.starts_new_run, `${sequence.name}: ${key}`);
      }
    }
  });
});

describe("terminal UI contract 1: open_url.json", () => {
  test("open_url opens or refuses each URL", () => {
    const cases = readCases<{ cases: { description: string; web_url: string | null; url: string; is_allowed: boolean }[] }>(
      "open_url.json",
    );
    for (const urlCase of cases.cases) {
      assert.equal(isOpenUrlAllowed(urlCase.web_url, urlCase.url), urlCase.is_allowed, `${urlCase.description}: ${urlCase.url}`);
    }
  });
});

describe("terminal UI contract 1: notices.json", () => {
  test("the launcher cuts each notice text as the terminal UI shows it", () => {
    // The repeat cases concern what the terminal UI hides; the launcher sends every notice.
    const cases = readCases<{ text_cases: { name: string; text: string; shown: string }[] }>("notices.json");
    for (const textCase of cases.text_cases) {
      assert.equal(noticeText(textCase.text), textCase.shown, textCase.name);
    }
  });
});

describe("terminal UI contract 1: environment.json", () => {
  test("the terminal UI gets only the allow-listed variables and the one auth variable", () => {
    const cases = readCases<{
      cases: { name: string; auth: TerminalUiAuth; launcher_environment: Record<string, string>; terminal_ui_environment: Record<string, string> }[];
    }>("environment.json");
    for (const environmentCase of cases.cases) {
      assert.deepEqual(
        buildTerminalUiEnvironment(environmentCase.launcher_environment, environmentCase.auth),
        environmentCase.terminal_ui_environment,
        environmentCase.name,
      );
    }
  });
});

describe("terminal UI contract 1: describe.json", () => {
  const cases = readCases<{
    cases: { name: string; describe: unknown; is_valid: boolean }[];
    negotiation: {
      name: string;
      launcher_contracts: number[];
      describe: never;
      mode: "chat" | "sessions" | "connectors";
      expected: { contract?: number; plain_reason?: string };
    }[];
  }>("describe.json");

  test("--describe output passes or fails the launcher's check", () => {
    for (const describeCase of cases.cases) {
      const parsed = parseDescription(Buffer.from(`${JSON.stringify(describeCase.describe)}\n`));
      assert.equal(typeof parsed === "object", describeCase.is_valid, describeCase.name);
    }
    assert.equal(typeof parseDescription(Buffer.from("not json\n")), "string");
    assert.equal(typeof parseDescription(Buffer.from("[1]\n")), "string");
  });

  test("a no_common_contract line names the side to upgrade, and none for a dexcli without contracts", () => {
    const detail = (contracts: readonly string[]): string => {
      const choice = chooseContract([1], {
        dexcli_version: "1.46.0",
        contracts: Object.fromEntries(contracts.map((version) => [version, { modes: { chat: {} } }])),
      }, "chat");
      assert.ok("plainReason" in choice && choice.plainReason === "no_common_contract");
      return choice.detail;
    };
    assert.equal(detail([]), "dexcli 1.46.0 offers no terminal UI contract");
    assert.match(detail(["2", "3"]), /this command knows\. Upgrade this command to use it$/);
    assert.match(detail(["0"]), /this command knows\. The terminal UI needs dexcli 1\.46\.0 or later; point DEXCLI at one$/);
  });

  test("the launcher chooses the highest common contract with the mode", () => {
    for (const negotiation of cases.negotiation) {
      const choice = chooseContract(negotiation.launcher_contracts, negotiation.describe, negotiation.mode);
      assert.deepEqual(
        "contract" in choice ? { contract: choice.contract } : { plain_reason: choice.plainReason },
        negotiation.expected,
        negotiation.name,
      );
    }
    assert.deepEqual(launcherContractVersions, [1]);
  });
});

describe("terminal UI contract 1: idempotency_table.json", () => {
  interface TableEvent {
    readonly at_ms: number;
    readonly op: "arrive" | "finish" | "query" | "fill";
    readonly key?: string;
    readonly state?: string;
    readonly prefix?: string;
    readonly count?: number;
  }
  const cases = readCases<{ cases: { name: string; events: TableEvent[] }[] }>("idempotency_table.json");

  test("the table remembers running keys, forgets finished ones after 10 minutes, and keeps 256", async () => {
    for (const tableCase of cases.cases) {
      let nowMs = 0;
      const table = new RequestOutcomeTable(() => nowMs);
      const finishers = new Map<string, (outcome: LocalRequestOutcome) => void>();
      const arrive = (key: string): void => {
        let finish: (outcome: LocalRequestOutcome) => void = () => undefined;
        const outcome = new Promise<LocalRequestOutcome>((resolve) => {
          finish = resolve;
        });
        const entry: RequestOutcomeEntry = { method: "put_image", paramsText: "{}", outcome };
        table.remember(key, entry);
        finishers.set(key, finish);
      };
      const finish = async (key: string): Promise<void> => {
        finishers.get(key)?.({ result: {} });
        // The table records the outcome when the operation's Promise settles.
        await Promise.resolve();
        await Promise.resolve();
      };
      for (const event of tableCase.events) {
        nowMs = event.at_ms;
        switch (event.op) {
          case "arrive":
            arrive(event.key ?? "");
            break;
          case "finish":
            await finish(event.key ?? "");
            break;
          case "fill":
            for (let index = 0; index < (event.count ?? 0); index++) {
              nowMs = event.at_ms + index;
              arrive(`${event.prefix ?? ""}${index}`);
              await finish(`${event.prefix ?? ""}${index}`);
            }
            break;
          case "query":
            assert.equal(table.state(event.key ?? ""), event.state, `${tableCase.name} at ${event.at_ms}: ${event.key ?? ""}`);
            break;
        }
      }
    }
  });
});

describe("terminal UI contract 1: messages.json", () => {
  const cases = readCases<{
    cases: { name: string; direction: "launcher" | "tui"; message: ChannelMessage; is_valid: boolean }[];
    result_cases: { name: string; method: string; result: unknown; is_valid: boolean }[];
  }>("messages.json");

  test("the launcher accepts each valid terminal UI message, skips later types, and refuses malformed ones", () => {
    for (const messageCase of cases.cases.filter((candidate) => candidate.direction === "tui")) {
      const responses: { id: number; outcome: LocalRequestOutcome }[] = [];
      const relay = new LocalRequestRelay(
        new Map([["put_image", () => Promise.resolve({ result: {} })]]),
        true,
        (id, outcome) => responses.push({ id, outcome }),
      );
      const handling = relay.handleMessage(messageCase.message);
      assert.equal(handling !== "malformed", messageCase.is_valid, messageCase.name);
      if (messageCase.message.type === "request" && !messageCase.is_valid && LocalRequestRelay.isRequestId(messageCase.message.id)) {
        assert.equal(responses[0]?.id, messageCase.message.id, messageCase.name);
      }
    }
  });

  test("the channel schema the launcher checks its messages with agrees with every case", () => {
    for (const messageCase of cases.cases) {
      const validate = terminalUiChannelValidator(messageCase.direction === "tui" ? "from_tui" : "from_launcher");
      assert.equal(validate(messageCase.message), messageCase.is_valid, messageCase.name);
    }
    for (const resultCase of cases.result_cases) {
      assert.equal(terminalUiChannelValidator(`${resultCase.method}_result`)(resultCase.result), resultCase.is_valid, resultCase.name);
    }
  });

  test("the launcher's own responses, notices, and Worker states are valid launcher messages", () => {
    const validate = terminalUiChannelValidator("from_launcher");
    for (const message of [
      { type: "response", id: 1, ...refusal("host_stopped", "The Worker host stopped.") },
      { type: "response", id: 2, result: { session_id: "scripted-20261111-142233-872f0d" } },
      { type: "notice", level: "warning", text: noticeText("a\nline") },
      { type: "worker", state: "stopping" },
      { type: "worker", state: "stopped", exit_code: 137 },
    ]) {
      assert.equal(validate(message), true, JSON.stringify(validate.errors));
    }
  });
});

describe("terminal UI contract 1: the launch document", () => {
  const launchDetails = {
    commandName: "dex-agents-scripted",
    harnessName: "scripted",
    commandVersion: "0.6.0",
    serverAddress: "127.0.0.1:18801",
    tls: { enabled: false, ca_file: null, server_name: null },
    auth: { mode: "none" } as const,
    webUrl: "http://127.0.0.1:18802",
    flowType: "DexAgentsCliScriptedSession-ab12cd",
    sessionId: "scripted-20261111-142233-a1b2c3",
    isResumed: false,
    model: "scripted/other",
    harnessDefaultModel: "scripted/default",
    suggestedModels: ["scripted/small", "scripted/other", "scripted/large"],
    workspace: "/home/user/dex-agent-workspace",
    configPath: "/home/user/.dex/agents/scripted.yaml",
    methods: ["put_image", "start_session", "list_connectors", "open_url", "get_request_result"],
    capabilities: {
      images: { max_per_message: 20, max_bytes: 3_750_000 },
      plan_mode: false,
      turn_control: true,
      reasoning_effort: { values: ["low", "medium", "high"] },
    },
    logPath: "/home/user/.dex/agents/scripted.log",
    homeDirectory: "/home/user",
  };

  test("dex.tls and dex.auth come from the command's connection settings, without the token file", () => {
    const tls = { enabled: true, ca_file: "/home/user/certs/ca.pem", server_name: "dex.internal" };
    const validate = terminalUiSchemaValidator("launch");
    const settings: readonly DexAuthSettings[] = [
      { mode: "api_key", token_url: "https://auth.example.com/token", server_id: "dex-prod" },
      { mode: "bearer", source: "env" },
      { mode: "bearer", source: "file", file: "/run/dex/token" },
    ];
    const variables = { DEX_API_KEY: "dex_key_1", DEX_CREDENTIAL: "token-1", DEX_CREDENTIAL_FILE: "/other/token" };
    const expected = [
      { auth: { mode: "api_key", token_url: "https://auth.example.com/token", server_id: "dex-prod" }, variable: { DEX_API_KEY: "dex_key_1" } },
      { auth: { mode: "bearer", source: "env" }, variable: { DEX_CREDENTIAL: "token-1" } },
      { auth: { mode: "bearer", source: "file" }, variable: { DEX_CREDENTIAL_FILE: "/run/dex/token" } },
    ];
    settings.forEach((auth, index) => {
      const document = chatLaunchDocument({
        ...launchDetails,
        serverAddress: "dex.example.com:443",
        tls,
        auth: terminalUiAuthOf(auth),
        startedDexcliDevLines: [],
        workerHostStartupLines: [],
      });
      assert.equal(validate(document), true, JSON.stringify(validate.errors));
      assert.deepEqual((document.dex as { tls: unknown; auth: unknown }).tls, tls);
      assert.deepEqual((document.dex as { auth: unknown }).auth, expected[index]?.auth);
      assert.deepEqual(terminalUiAuthVariables(auth, variables), expected[index]?.variable);
    });
  });

  test("display.models lists the configured model, the harness default, then its suggestions, each once", () => {
    const document = chatLaunchDocument({ ...launchDetails, startedDexcliDevLines: [], workerHostStartupLines: [] });
    const validate = terminalUiSchemaValidator("launch");
    assert.equal(validate(document), true, JSON.stringify(validate.errors));
    assert.deepEqual((document.display as { models: unknown }).models, [
      "scripted/other",
      "scripted/default",
      "scripted/small",
      "scripted/large",
    ]);
    const withoutDefault = chatLaunchDocument({
      ...launchDetails,
      model: "scripted/default",
      harnessDefaultModel: null,
      suggestedModels: [],
      startedDexcliDevLines: [],
      workerHostStartupLines: [],
    });
    assert.deepEqual((withoutDefault.display as { models: unknown }).models, ["scripted/default"]);
  });

  test("the chat launch document passes the launch schema and writes paths with ~", () => {
    const document = chatLaunchDocument({
      ...launchDetails,
      startedDexcliDevLines: ["Starting dexcli dev\tnow", "dexcli dev is ready (pid 42)."],
      workerHostStartupLines: ["x".repeat(600)],
    });
    const validate = terminalUiSchemaValidator("launch");
    assert.equal(validate(document), true, JSON.stringify(validate.errors));
    assert.deepEqual(document.display, {
      model: "scripted/other",
      workspace: "~/dex-agent-workspace",
      config_path: "~/.dex/agents/scripted.yaml",
      models: ["scripted/other", "scripted/default", "scripted/small", "scripted/large"],
    });
    assert.equal(document.log_path, "~/.dex/agents/scripted.log");
    // The dexcli dev lines come first, as info; the Worker host's startup lines follow, as warnings.
    assert.deepEqual(document.notices, [
      { level: "info", text: "Starting dexcli dev now" },
      { level: "info", text: "dexcli dev is ready (pid 42)." },
      { level: "warning", text: `${"x".repeat(499)}…` },
    ]);
  });

  test("many long multi-byte startup lines keep the launch document within its notice budget", () => {
    const lines = Array.from({ length: 3_000 }, (_, index) => `${index} ${"界".repeat(600)}`);
    const document = chatLaunchDocument({ ...launchDetails, startedDexcliDevLines: ["started"], workerHostStartupLines: lines });
    const notices = document.notices as { level: string; text: string }[];
    const noticeBytes = Buffer.byteLength(JSON.stringify(notices.slice(0, -1)), "utf8");
    assert.ok(noticeBytes <= maxLaunchNoticeBytes, `${noticeBytes} bytes`);
    assert.ok(Buffer.byteLength(JSON.stringify(document), "utf8") < 128 * 1024);
    assert.deepEqual(notices[0], { level: "info", text: "started" });
    // Each kept line is cut to 500 code points of 3 bytes, about 1.5 KB of JSON, so about 43 fit.
    const kept = notices.length - 2;
    assert.ok(kept > 30 && kept < 50, `${kept} lines kept`);
    assert.ok(notices[1]?.text.startsWith("0 界"));
    assert.deepEqual(notices.at(-1), {
      level: "warning",
      text: `… ${3_001 - (kept + 1)} more lines in ~/.dex/agents/scripted.log`,
    });
    const validate = terminalUiSchemaValidator("launch");
    assert.equal(validate(document), true, JSON.stringify(validate.errors));
  });
});

describe("terminal UI selection", () => {
  const selected = {
    platform: "darwin",
    isPlainRequested: false,
    isInteractive: true,
    term: "xterm-256color",
    harnessTerminalInterface: "dexcli",
  } as const;

  test("only an interactive, non-Windows run without --plain, with a TERM other than dumb, of a dexcli harness hands off", () => {
    assert.equal(isTerminalUiSelected(selected), true);
    assert.equal(isTerminalUiSelected({ ...selected, platform: "linux" }), true);
    for (const refused of [
      { ...selected, platform: "win32" as const },
      { ...selected, isPlainRequested: true },
      { ...selected, isInteractive: false },
      { ...selected, term: "dumb" },
      { ...selected, term: undefined },
      { ...selected, term: "" },
      { ...selected, harnessTerminalInterface: "plain" as const },
    ]) {
      assert.equal(isTerminalUiSelected(refused), false, JSON.stringify(refused));
    }
  });
});

describe("leftover cleanup", () => {
  test("checks each group right before its own signal, so a group whose leader ended meanwhile gets none", async () => {
    const identity = (pid: number): ProcessIdentity => ({
      pid,
      pgid: pid,
      start_time: "Wed Nov 11 18:00:00 2026",
      command_sha256: "0".repeat(64),
    });
    const host = identity(100);
    const tool = identity(200);
    const signals: string[] = [];
    let isToolLeaderAlive = true;
    const stopped = await stopRecordedGroups([host, tool], host, {
      isLeader: (group) => group === host || isToolLeaderAlive,
      signal: (pgid, signal) => {
        signals.push(`${signal} ${pgid}`);
        // Stopping the Worker host ends the tool group's leader before its turn.
        if (pgid === host.pgid) {
          isToolLeaderAlive = false;
        }
      },
      waitForGrace: () => Promise.resolve(),
    });
    assert.deepEqual(signals, ["SIGTERM 100", "SIGKILL 100"]);
    assert.deepEqual(stopped, { isHostStopped: true, toolGroupCount: 0 });
  });

  test("checks again before SIGKILL", async () => {
    const host: ProcessIdentity = { pid: 100, pgid: 100, start_time: "x", command_sha256: "0".repeat(64) };
    const signals: string[] = [];
    let isAlive = true;
    await stopRecordedGroups([host], host, {
      isLeader: () => isAlive,
      signal: (pgid, signal) => signals.push(`${signal} ${pgid}`),
      waitForGrace: () => {
        isAlive = false;
        return Promise.resolve();
      },
    });
    assert.deepEqual(signals, ["SIGTERM 100"]);
  });
});

describe("launcher output", () => {
  test("the CLI core's own code never writes to file descriptors 1 and 2 directly", () => {
    // A JavaScript stream write is redirected during the handoff; a direct fd write would reach the terminal UI.
    const sourceDirectory = fileURLToPath(new URL("../../src/", import.meta.url));
    const sources = listJavaScript(sourceDirectory);
    assert.ok(sources.length > 10);
    for (const path of sources) {
      const text = readFileSync(path, "utf8");
      assert.doesNotMatch(text, /\b(?:writeSync|write|writev)\(\s*[12]\s*,/, path);
      assert.doesNotMatch(text, /createWriteStream\([^)]*fd:\s*[12]\b/, path);
    }
  });

  test("no child the launcher starts, except the terminal UI, inherits stdout or stderr", () => {
    // Local tools run only in the Worker host and the plain chat, whose output is not under a terminal UI.
    const sourceDirectory = fileURLToPath(new URL("../../src/", import.meta.url));
    const sources = listJavaScript(sourceDirectory).filter((path) => !path.includes("/local-tools/"));
    let terminalUiSpawns = 0;
    for (const path of sources) {
      const text = readFileSync(path, "utf8");
      assert.doesNotMatch(text, /stdio:\s*"inherit"/, path);
      for (const match of text.matchAll(/stdio:\s*\[([^\]]*)\]/g)) {
        const entries = (match[1] ?? "").split(",").map((entry) => entry.trim());
        if (entries.slice(1, 3).includes('"inherit"')) {
          assert.ok(path.endsWith("terminal-ui-chat-launcher.js"), `${path}: ${match[0]}`);
          terminalUiSpawns += 1;
        }
      }
    }
    assert.equal(terminalUiSpawns, 1);
  });
});

function listJavaScript(directory: string): string[] {
  return readdirSync(directory).flatMap((name) => {
    const path = join(directory, name);
    return statSync(path).isDirectory() ? listJavaScript(path) : name.endsWith(".js") ? [path] : [];
  });
}
