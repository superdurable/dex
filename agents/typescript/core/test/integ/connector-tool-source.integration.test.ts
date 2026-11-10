// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// ConnectorToolSource without an engine: definitions, mapping, approval, and the fence context. The twin of the
// Python core's tests/test_connector_tool_source.py.

import assert from "node:assert/strict";
import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, test } from "node:test";

import type { AsyncContext } from "@superdurable/dex";
import { createCall, deriveCallID, type Call } from "@superdurable/connectors-sdk";
import { Fence } from "@superdurable/connectors-sdk/singledispatch";

import {
  ConnectorStepContext,
  ConnectorToolSource,
  boundConnectorResult,
  checkConnectorInputSchema,
  classifyConnectorBranch,
  connectorTool,
  findConnectorInputViolation,
  formatConnectorResult,
  type ConnectorBranchOutcome,
  type ConnectorPackageManifest,
} from "../../src/connectors/index.js";
import { isProvenNotSent, lowerCamelToSnake, mayHaveDispatched } from "../../src/connectors/connector-tool-source.js";
import type { AgentToolCall, AgentTurnContext, ToolPolicy } from "../../src/index.js";
import {
  byIdManifest,
  ledgerConnection,
  previousByIdToolName,
  readFakeLedgerManifest,
  retriedNoteManifest,
} from "../fixtures/connector-test-agent.js";
import { conformanceSuiteDirectory } from "../fixtures/conformance-suite.js";
import {
  CloseAccountInputSchema,
  CloseAccountOperation,
  CreatePayoutInputSchema,
  CreatePayoutOperation,
  FakeLedgerProvider,
  GetBalanceByIdOperation,
  GetBalanceInputSchema,
  GetBalanceOperation,
  PostNoteInputSchema,
  PostNoteOperation,
} from "../fixtures/fake-ledger.js";

interface ConformanceCases {
  readonly inputSchema: Readonly<Record<string, unknown>>;
  readonly argumentCases: readonly { readonly arguments: unknown; readonly violation: string | null }[];
  readonly refusedSchemas: readonly { readonly schema: Readonly<Record<string, unknown>>; readonly error: string }[];
  readonly branchCases: readonly {
    readonly kind: "query" | "mutation";
    readonly branch: string;
    readonly requiredBranches: readonly string[];
    readonly agentBranches: Readonly<Record<string, string>>;
    readonly mayHaveDispatched?: boolean;
    readonly outcome: ConnectorBranchOutcome;
  }[];
  readonly toolNameCases: readonly { readonly operationId: string; readonly snakeName: string }[];
  readonly fenceObservationCases: readonly {
    readonly name: string;
    readonly attempt: number;
    readonly previousCheckpoint: string | null;
    readonly readsCheckpoint: boolean;
    readonly savedHeartbeats: readonly unknown[];
    readonly mayHaveDispatched: boolean;
    readonly isProvenNotSent: boolean;
  }[];
  readonly resultCases: readonly { readonly json: string; readonly text: string }[];
  readonly truncationCases: readonly {
    readonly output: string;
    readonly maxResultBytes: number;
    readonly expectedOutput: string;
    readonly truncatedOutputBytes: number | null;
  }[];
}

const conformance = JSON.parse(
  readFileSync(join(conformanceSuiteDirectory, "connector_tools.json"), "utf8"),
) as ConformanceCases;
const provider = new FakeLedgerProvider(mkdtempSync(join(tmpdir(), "fake-ledger-")));
const manifest = readFakeLedgerManifest();

describe("connector tools follow the shared conformance cases", () => {
  test("arguments are checked as the conformance suite says", () => {
    for (const testCase of conformance.argumentCases) {
      assert.equal(
        findConnectorInputViolation(testCase.arguments, conformance.inputSchema) ?? null,
        testCase.violation,
        JSON.stringify(testCase.arguments),
      );
    }
  });

  test("a schema with an unchecked keyword is refused, and the conformance schema is accepted", () => {
    for (const testCase of conformance.refusedSchemas) {
      assert.throws(() => checkConnectorInputSchema(testCase.schema), { message: testCase.error });
    }
    checkConnectorInputSchema(conformance.inputSchema);
  });

  test("branches map as the conformance suite says", () => {
    for (const testCase of conformance.branchCases) {
      assert.equal(
        classifyConnectorBranch({
          kind: testCase.kind,
          branch: testCase.branch,
          requiredBranches: new Set(testCase.requiredBranches),
          agentBranches: testCase.agentBranches,
          mayHaveDispatched: testCase.mayHaveDispatched ?? false,
        }),
        testCase.outcome,
        JSON.stringify(testCase),
      );
    }
  });

  test("operation IDs become snake case as the conformance suite says", () => {
    for (const testCase of conformance.toolNameCases) {
      assert.equal(lowerCamelToSnake(testCase.operationId), testCase.snakeName, testCase.operationId);
    }
  });

  test("fence events are observed as the conformance suite says", async () => {
    const operation = { connectorId: "fake-ledger", operationId: "postNote" };
    const callId = deriveCallID("session-1", "CallTool-3", operation);
    const otherCallId = deriveCallID("session-1", "CallTool-4", operation);
    const bind = (value: string) => value.replaceAll("<callId>", callId).replaceAll("<otherCallId>", otherCallId);
    for (const testCase of conformance.fenceObservationCases) {
      const previous = testCase.previousCheckpoint === null ? undefined : bind(testCase.previousCheckpoint);
      const fenced = fencedCallWithContext(testCase.attempt, previous, new SessionCheckpoints());
      if (testCase.readsCheckpoint) {
        fenced.context.hasLastHeartbeatValue();
      }
      for (const saved of testCase.savedHeartbeats) {
        await fenced.context.recordHeartbeat(JSON.parse(bind(JSON.stringify(saved))) as unknown);
      }
      assert.equal(mayHaveDispatched(fenced.call, fenced.context), testCase.mayHaveDispatched, testCase.name);
      assert.equal(isProvenNotSent(fenced.call, fenced.context), testCase.isProvenNotSent, testCase.name);
    }
  });

  test("result text is the same in every core", () => {
    for (const testCase of conformance.resultCases) {
      assert.equal(formatConnectorResult(testCase.json), testCase.text);
    }
  });

  test("results are truncated as the conformance suite says", () => {
    for (const testCase of conformance.truncationCases) {
      const result = boundConnectorResult(testCase.output, testCase.maxResultBytes);
      assert.equal(result.output, testCase.expectedOutput);
      assert.equal(result.truncatedOutputBytes ?? null, testCase.truncatedOutputBytes);
    }
  });
});

describe("connector tools come from the manifest", () => {
  test("each operation kind maps to its effect and Step options", () => {
    const query = connectorTool(new GetBalanceOperation(provider), ledgerConnection, {
      manifest,
      inputValidator: GetBalanceInputSchema,
    });
    const safeWrite = connectorTool(new CreatePayoutOperation(provider), ledgerConnection, {
      manifest,
      inputValidator: CreatePayoutInputSchema,
    });
    const singleWrite = connectorTool(new PostNoteOperation(provider), ledgerConnection, {
      manifest,
      inputValidator: PostNoteInputSchema,
    });
    assert.deepEqual([query.name, query.effect], ["fake-ledger_get_balance", "read"]);
    assert.deepEqual([safeWrite.name, safeWrite.effect], ["fake-ledger_create_payout", "idempotent_write"]);
    assert.equal(singleWrite.effect, "non_idempotent_write");
    assert.equal(query.description, "Read the balance of one ledger account.");
    assert.equal(safeWrite.isApprovalRecommended, true);
    assert.equal(singleWrite.isApprovalRecommended, false);
    for (const tool of [query, safeWrite, singleWrite]) {
      assert.equal(tool.executeMethodTimeout, 30_000);
      // The connector policy's one-hour window, narrowed to the tool Steps' three attempts.
      assert.equal(tool.retryPolicy.maximumAttempts, 3);
      assert.equal(tool.retryPolicy.totalDurationMs, 3_600_000);
    }
  });

  test("an explicit name keeps a previous default tool name", () => {
    const operation = new GetBalanceByIdOperation(provider);
    const options = { manifest: byIdManifest(), inputValidator: GetBalanceInputSchema };
    assert.equal(connectorTool(operation, ledgerConnection, options).name, "fake-ledger_get_balance_by_id");
    const kept = connectorTool(operation, ledgerConnection, { ...options, name: previousByIdToolName });
    assert.equal(kept.name, "fake-ledger_get_balance_by_i_d");
  });

  test("an operation without agent.exposed is refused", () => {
    assert.throws(
      () => connectorTool(new CloseAccountOperation(), ledgerConnection, { manifest, inputValidator: CloseAccountInputSchema }),
      { message: /closeAccount is not exposed to agents/ },
    );
    const declined = mutableManifest();
    declined.factories[0]!.agent!["exposed"] = false;
    assert.throws(
      () =>
        connectorTool(new GetBalanceOperation(provider), ledgerConnection, {
          manifest: declined as unknown as ConnectorPackageManifest,
          inputValidator: GetBalanceInputSchema,
        }),
      { message: /getBalance is not exposed to agents/ },
    );
  });

  test("a manifest entry the core cannot honour is refused", () => {
    const cases: readonly [(factory: MutableFactory) => void, RegExp][] = [
      [(factory) => (factory.agent!["approval"] = "always"), /unknown agent.approval/],
      [(factory) => (factory.agent!["retry"] = "maybe"), /unknown agent.retry/],
      [(factory) => (factory.agent!["branches"] = { notFound: "fine" }), /agent.branches values must be rejected or unknown/],
      [(factory) => (factory.agent!["branches"] = { paid: "rejected" }), /agent.branches may list only optional branches/],
      [(factory) => (factory.kind = "query"), /the manifest says query/],
      [(factory) => (factory.inputSchema.properties["accountId"]!["pattern"] = "x"), /uses pattern/],
    ];
    for (const [change, message] of cases) {
      const changed = mutableManifest();
      change(changed.factories[1]!);
      assert.throws(
        () =>
          connectorTool(new CreatePayoutOperation(provider), ledgerConnection, {
            manifest: changed as unknown as ConnectorPackageManifest,
            inputValidator: CreatePayoutInputSchema,
          }),
        { message },
      );
    }
    const unknownQuery = mutableManifest();
    unknownQuery.factories[0]!.agent!["branches"] = { invalidResponse: "unknown" };
    assert.throws(
      () =>
        connectorTool(new GetBalanceOperation(provider), ledgerConnection, {
          manifest: unknownQuery as unknown as ConnectorPackageManifest,
          inputValidator: GetBalanceInputSchema,
        }),
      { message: /agent.branches unknown is for Mutations only/ },
    );
  });

  test("two tools with one name are refused", () => {
    const tool = connectorTool(new GetBalanceOperation(provider), ledgerConnection, {
      manifest,
      inputValidator: GetBalanceInputSchema,
    });
    assert.throws(() => new ConnectorToolSource([tool, tool]), { message: /share a name/ });
  });
});

describe("connector tool approval", () => {
  const payouts = new CreatePayoutOperation(provider);
  const payoutTool = (name: string, requiresApproval?: boolean | ((args: Readonly<Record<string, unknown>>) => boolean)) =>
    connectorTool(payouts, ledgerConnection, {
      manifest,
      inputValidator: CreatePayoutInputSchema,
      name,
      ...(requiresApproval === undefined ? {} : { requiresApproval }),
    });
  const source = new ConnectorToolSource([
    payoutTool("recommended"),
    payoutTool("never", false),
    connectorTool(new PostNoteOperation(provider), ledgerConnection, { manifest, inputValidator: PostNoteInputSchema, name: "note" }),
    payoutTool("limited", (args) => Number(args["amount"]) > 50),
    payoutTool("broken", () => {
      throw new Error("no limit configured");
    }),
  ]);
  const approval = (name: string, toolArguments: unknown, toolPolicies?: Record<string, ToolPolicy>) =>
    source.deferredToolCall(turn(toolPolicies), {
      toolCallId: "call-1",
      toolName: name,
      arguments: JSON.stringify(toolArguments),
      listedTool: { name, description: "", inputSchema: {} },
    }).policy.requiresApproval;

  test("approval follows the manifest unless the application or session overrides it", () => {
    const payout = { accountId: "a", amount: 10 };
    assert.equal(approval("recommended", payout), true);
    assert.equal(approval("never", payout), false);
    // Mutations run without approval by default, though non_idempotent_write asks by default.
    assert.equal(approval("note", { accountId: "a", text: "hi" }), false);
    assert.equal(approval("limited", payout), false);
    assert.equal(approval("limited", { accountId: "a", amount: 51 }), true);
    // Arguments the tool Step refuses never run, so they never wait.
    assert.equal(approval("limited", { accountId: "a" }), false);
    assert.equal(approval("broken", payout), true);
    assert.equal(approval("recommended", payout, { recommended: { effect: "read", requiresApproval: false } }), false);
  });

  test("a session policy never changes the effect class", () => {
    const notes = new ConnectorToolSource([
      connectorTool(new PostNoteOperation(provider), ledgerConnection, { manifest, inputValidator: PostNoteInputSchema }),
    ]);
    const call = notes.deferredToolCall(turn({ "fake-ledger_post_note": { effect: "read" } }), {
      toolCallId: "call-1",
      toolName: "fake-ledger_post_note",
      arguments: "{}",
      listedTool: { name: "fake-ledger_post_note", description: "", inputSchema: {} },
    });
    assert.equal(call.policy.effect, "non_idempotent_write");
    assert.equal(call.ordered, true);
    assert.equal(call.toolSourceReference, '{"tool": "fake-ledger_post_note"}');
    const record = {
      toolCallId: "call-1",
      turnId: "turn-1",
      toolName: "fake-ledger_post_note",
      arguments: "{}",
      effect: "non_idempotent_write",
      requiresApproval: false,
      status: "approved",
      output: null,
      error: null,
      rejectionMessage: null,
      deferralOrder: 0,
      toolSourceReference: call.toolSourceReference!,
    } satisfies AgentToolCall;
    assert.equal(notes.toolCallStepOptions(record)?.executeMethodTimeout, 30_000);
    assert.equal(retriedNoteManifest().factories[2]?.agent?.retry, "safe");
  });
});

describe("the Step context the single-dispatch fence uses", () => {
  test("it exposes only what the fence reads, plus the Call identities", () => {
    const members = Object.getOwnPropertyNames(ConnectorStepContext.prototype)
      .filter((name) => name !== "constructor")
      .sort();
    assert.deepEqual(members, [
      "attempt",
      "cancellationSignal",
      "deadline",
      "flowId",
      "getLastHeartbeatValue",
      "hasLastHeartbeatValue",
      "recordHeartbeat",
      "runId",
      "stepExecutionId",
    ]);
  });

  test("every fence method keeps its marker through the session checkpoint", async () => {
    const first = new SessionCheckpoints();
    const fence = new Fence(fencedCall(1, undefined, first), 30_000);
    assert.equal(fence.decide().kind, "sendAllowed");
    const sent = await fence.dispatch(async () => {
      // A bare background heartbeat now repeats the claim instead of clearing it.
      await first.record();
      return "note-1";
    });
    assert.equal(sent, "note-1");
    await fence.recordProviderId("note-1");
    assert.notEqual(first.checkpoint, undefined);
    assert.ok(first.recorded.every((value) => value !== undefined));

    const decision = new Fence(fencedCall(2, first.checkpoint, new SessionCheckpoints()), 30_000).decide();
    assert.equal(decision.kind, "readBackRequired");
    assert.equal(decision.marker?.providerId, "note-1");
  });

  test("a marker proving no send lets the next attempt send", async () => {
    const first = new SessionCheckpoints();
    const fence = new Fence(fencedCall(1, undefined, first), 30_000);
    fence.decide();
    await fence.recordNotSent();
    const decision = new Fence(fencedCall(2, first.checkpoint, new SessionCheckpoints()), 30_000).decide();
    assert.equal(decision.kind, "sendAllowed");
    assert.equal(decision.marker?.state, "notSent");

    const resent = new SessionCheckpoints();
    const rejectedFence = new Fence(fencedCall(1, undefined, resent), 30_000);
    rejectedFence.decide();
    await rejectedFence.dispatch(async () => undefined);
    await rejectedFence.recordCanResend();
    assert.equal(new Fence(fencedCall(2, resent.checkpoint, new SessionCheckpoints()), 30_000).decide().kind, "sendAllowed");
  });

  test("a checkpoint that is not a marker fails closed, and the text matches the Python core", () => {
    assert.equal(new Fence(fencedCall(2, '"a task handle"', new SessionCheckpoints()), 30_000).decide().kind, "failClosed");
    assert.equal(new Fence(fencedCall(2, "not json", new SessionCheckpoints()), 30_000).decide().kind, "failClosed");
  });

  test("the stored marker is the Python core's text", async () => {
    const checkpoints = new SessionCheckpoints();
    const call = fencedCall(1, undefined, checkpoints);
    const fence = new Fence(call, 30_000);
    fence.decide();
    await fence.recordNotSent();
    assert.equal(checkpoints.checkpoint, `{"attempt":1,"callId":"${call.id}","state":"notSent"}`);
  });

  test("only a saved notSent or canResend marker of this attempt proves no send", async () => {
    const proves = (fenced: FencedCall) => isProvenNotSent(fenced.call, fenced.context);
    assert.equal(proves(fencedCallWithContext(1, undefined, new SessionCheckpoints())), false);

    const notSent = fencedCallWithContext(1, undefined, new SessionCheckpoints());
    let fence = new Fence(notSent.call, 30_000);
    fence.decide();
    await fence.recordNotSent();
    assert.equal(proves(notSent), true);

    const claimed = fencedCallWithContext(1, undefined, new SessionCheckpoints());
    fence = new Fence(claimed.call, 30_000);
    fence.decide();
    await fence.dispatch(async () => "note-1");
    assert.equal(proves(claimed), false);
    await fence.recordCanResend();
    assert.equal(proves(claimed), true);

    const accepted = fencedCallWithContext(1, undefined, new SessionCheckpoints());
    fence = new Fence(accepted.call, 30_000);
    fence.decide();
    await fence.dispatch(async () => "note-1");
    await fence.recordProviderId("note-1");
    assert.equal(proves(accepted), false);

    // A marker an earlier attempt saved is history, not proof about this attempt.
    const first = new SessionCheckpoints();
    fence = new Fence(fencedCallWithContext(1, undefined, first).call, 30_000);
    fence.decide();
    await fence.recordNotSent();
    assert.equal(proves(fencedCallWithContext(2, first.checkpoint, new SessionCheckpoints())), false);
  });

  test("only fence checkpoints proving no send keep the manifest listing", async () => {
    const dispatched = (fenced: FencedCall, _previous: string | undefined) =>
      mayHaveDispatched(fenced.call, fenced.context);
    assert.equal(dispatched(fencedCallWithContext(2, undefined, new SessionCheckpoints()), undefined), false);

    const notSent = new SessionCheckpoints();
    const notSentCall = fencedCallWithContext(1, undefined, notSent);
    let fence = new Fence(notSentCall.call, 30_000);
    fence.decide();
    assert.equal(dispatched(notSentCall, undefined), false);
    await fence.recordNotSent();
    assert.equal(dispatched(notSentCall, undefined), false);

    const claimed = new SessionCheckpoints();
    const claimedCall = fencedCallWithContext(1, undefined, claimed);
    fence = new Fence(claimedCall.call, 30_000);
    fence.decide();
    await fence.dispatch(async () => "note-1");
    assert.equal(dispatched(claimedCall, undefined), true);
    await fence.recordProviderId("note-1");
    assert.equal(dispatched(claimedCall, undefined), true);

    const canResend = new SessionCheckpoints();
    const canResendCall = fencedCallWithContext(1, undefined, canResend);
    fence = new Fence(canResendCall.call, 30_000);
    fence.decide();
    await fence.dispatch(async () => undefined);
    await fence.recordCanResend();
    assert.equal(dispatched(canResendCall, undefined), false);

    const callId = notSentCall.call.id;
    const previousCheckpoints: readonly (readonly [string, string | undefined, boolean])[] = [
      ["a notSent marker of the attempt before", notSent.checkpoint, false],
      ["a canResend marker of the attempt before", canResend.checkpoint, false],
      ["a claim", claimed.checkpoint, true],
      ["a marker of another call", notSent.checkpoint?.replace(callId, "f".repeat(callId.length)), true],
      ["a malformed marker", '{"callId": 7, "state": "notSent"}', true],
      ["a checkpoint that is not a marker", '"a task handle"', true],
      ["no checkpoint", undefined, true],
    ];
    for (const [label, previous, expected] of previousCheckpoints) {
      const retry = fencedCallWithContext(2, previous, new SessionCheckpoints());
      new Fence(retry.call, 30_000).decide();
      assert.equal(dispatched(retry, previous), expected, label);
    }
    const stale = fencedCallWithContext(3, notSent.checkpoint, new SessionCheckpoints());
    new Fence(stale.call, 30_000).decide();
    assert.equal(dispatched(stale, notSent.checkpoint), true);
  });

  test("a marker that failed to save proves nothing", async () => {
    const failing = new SessionCheckpoints();
    failing.save = async () => {
      throw new Error("the heartbeat did not reach Dex");
    };
    const fenced = fencedCallWithContext(1, undefined, failing);
    const fence = new Fence(fenced.call, 30_000);
    fence.decide();
    await assert.rejects(fence.recordNotSent());
    assert.equal(isProvenNotSent(fenced.call, fenced.context), false);
  });

  test("the Call ID depends only on the Flow, the Step execution, and the operation", () => {
    const first = fencedCall(1, undefined, new SessionCheckpoints());
    const retry = fencedCall(2, undefined, new SessionCheckpoints());
    assert.equal(first.id, retry.id);
    assert.equal(first.id, deriveCallID("session-1", "CallTool-3", { connectorId: "fake-ledger", operationId: "postNote" }));
  });
});

interface MutableFactory {
  kind: string;
  agent?: Record<string, unknown>;
  inputSchema: { properties: Record<string, Record<string, unknown>> };
}

function mutableManifest(): { factories: MutableFactory[] } {
  return readFakeLedgerManifest() as unknown as { factories: MutableFactory[] };
}

function turn(toolPolicies?: Record<string, ToolPolicy>): AgentTurnContext {
  return { ...(toolPolicies === undefined ? {} : { toolPolicies }) } as unknown as AgentTurnContext;
}

/** The session's checkpoint behavior: each heartbeat repeats the latest text checkpoint. */
class SessionCheckpoints {
  public readonly recorded: (string | undefined)[] = [];
  public checkpoint: string | undefined;

  public async record(): Promise<void> {
    this.recorded.push(this.checkpoint);
  }

  public async save(checkpoint: string): Promise<void> {
    this.checkpoint = checkpoint;
    await this.record();
  }
}

interface FencedCall {
  readonly call: Call;
  readonly context: ConnectorStepContext;
}

function fencedCall(attempt: number, previous: string | undefined, checkpoints: SessionCheckpoints): Call {
  return fencedCallWithContext(attempt, previous, checkpoints).call;
}

function fencedCallWithContext(attempt: number, previous: string | undefined, checkpoints: SessionCheckpoints): FencedCall {
  const live = {
    flowId: "session-1",
    runId: "run-1",
    stepExecutionId: "CallTool-3",
    attempt,
    deadline: new Date(Date.now() + 60_000),
    cancellationSignal: new AbortController().signal,
  } as unknown as AsyncContext;
  const context = new ConnectorStepContext(live, {
    previousCheckpoint: previous,
    heartbeat: () => checkpoints.record(),
    saveCheckpoint: (checkpoint) => checkpoints.save(checkpoint),
  });
  const call = createCall(context as unknown as AsyncContext, ledgerConnection, {
    connectorId: "fake-ledger",
    operationId: "postNote",
  });
  return { call, context };
}
