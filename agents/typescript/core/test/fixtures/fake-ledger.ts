// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// A fake Connector package, the TypeScript twin of the Python core's tests/fake_ledger: a ledger provider kept
// in files, with Connector SDK operations. Its manifest is fake-ledger-dex-connector.json, a copy of the Python one.
//
// getBalance is a Query. createPayout is a Stripe-like Mutation: the provider drops a repeated idempotency key, so
// it is retry: safe. postNote is a Slack-like Mutation guarded by the single-dispatch fence; the provider does not
// drop repeats. closeAccount is not exposed to agents. Words in a payout memo or a note text steer the provider:
// reject, invalid, uncertain, rate-limit-once (refused before the claim, notSent), rate-limit-after-send (the
// provider refuses the sent note, canResend), hide (read-back finds nothing), and gate-before-send:<gate>,
// gate-after-claim:<gate>, gate-after-accept:<gate>, or gate-before-can-resend:<gate>, which wait at a FileGate on
// attempt 1 only.

import { appendFileSync, closeSync, existsSync, fsyncSync, openSync, readFileSync } from "node:fs";
import { join } from "node:path";

import {
  SecretString,
  StaticCredentialProvider,
  newMutationBranch,
  newMutationRetry,
  newMutationUncertain,
  newQueryBranch,
  type Call,
  type ConnectionRef,
  type Failure,
  type Mutation,
  type MutationAttempt,
  type Query,
  type QueryAttempt,
  type QueryDefinition,
} from "@superdurable/connectors-sdk";
import { Fence } from "@superdurable/connectors-sdk/singledispatch";
import { z } from "zod";

import { FileGate } from "../../src/testing/index.js";
import manifest from "./fake-ledger-dex-connector.json" with { type: "json" };

export const fakeLedgerManifest = manifest;
export const fakeLedgerConnectorId = "fake-ledger";
export const fakeLedgerStateDirectoryVariable = "FAKE_LEDGER_STATE_DIR";
// The Worker reads the ledger connection's secret from this variable, as a real Worker reads its store.
export const fakeLedgerSecretVariable = "FAKE_LEDGER_SECRET";
export const fakeLedgerDefaultSecret = "unset-test-secret";
export const fakeLedgerConnection: ConnectionRef = { provider: fakeLedgerConnectorId, name: "test-ledger" };

export const GetBalanceInputSchema = z.strictObject({ accountId: z.string(), padding: z.number().int().optional() });
export const CreatePayoutInputSchema = z.strictObject({
  accountId: z.string(),
  amount: z.number().int(),
  memo: z.string().optional(),
});
export const PostNoteInputSchema = z.strictObject({ accountId: z.string(), text: z.string() });
export const CloseAccountInputSchema = z.strictObject({ accountId: z.string() });

type GetBalanceInput = z.infer<typeof GetBalanceInputSchema>;
type CreatePayoutInput = z.infer<typeof CreatePayoutInputSchema>;
type PostNoteInput = z.infer<typeof PostNoteInputSchema>;
type CloseAccountInput = z.infer<typeof CloseAccountInputSchema>;

interface Balance {
  readonly accountId: string;
  readonly amount: number;
  readonly filler?: string;
}
interface Payout {
  readonly payoutId?: string;
  readonly amount?: number;
}
interface Note {
  readonly noteId?: string;
}

function definition(operationId: string, ...branches: string[]): QueryDefinition {
  return {
    operation: { connectorId: fakeLedgerConnectorId, operationId },
    branches: branches.map((id, index) => ({ id, description: `The ${id} branch.`, ...(index > 0 ? { optional: true } : {}) })),
    stepDefaults: { executeMethodTimeoutMs: 30_000, executeDurability: "sync" },
  };
}

const getBalanceDefinition = definition("getBalance", "found", "notFound", "invalidResponse", "defect");
const createPayoutDefinition = definition("createPayout", "paid", "providerRejected", "uncertain", "invalidResponse", "defect");
const postNoteDefinition = definition("postNote", "posted", "providerRejected", "uncertain", "defect");
const closeAccountDefinition = definition("closeAccount", "closed", "defect");

interface ProviderEntry {
  readonly operation: string;
  readonly key: string;
  readonly authorized?: string;
  readonly id?: string;
  readonly hidden?: string;
}

/** The provider: an append-only file of writes in a state directory. */
export class FakeLedgerProvider {
  private readonly writesPath: string;
  private readonly sendsPath: string;
  private readonly expectedSecret: string;
  private readonly credentials: StaticCredentialProvider<SecretString>;

  public constructor(public readonly stateDirectory: string) {
    this.writesPath = join(stateDirectory, "fake-ledger-writes.jsonl");
    this.sendsPath = join(stateDirectory, "fake-ledger-sends.jsonl");
    const secret = process.env[fakeLedgerSecretVariable] ?? fakeLedgerDefaultSecret;
    this.expectedSecret = secret;
    this.credentials = new StaticCredentialProvider([[fakeLedgerConnection, new SecretString(secret)]]);
  }

  /** Resolves the connection's secret at call time and checks it, as a provider checks a token. */
  public async authorize(call: Call): Promise<boolean> {
    const secret = await this.credentials.resolve(call);
    return secret.reveal() === this.expectedSecret;
  }

  /** Every write that took effect, oldest first. */
  public writes(operation?: string): ProviderEntry[] {
    return readLines(this.writesPath).filter((entry) => operation === undefined || entry.operation === operation);
  }

  /** Every request that reached the provider, including repeats it dropped. */
  public sends(operation?: string): ProviderEntry[] {
    return readLines(this.sendsPath).filter((entry) => operation === undefined || entry.operation === operation);
  }

  public recordRateLimited(key: string): void {
    appendLine(this.sendsPath, { operation: "rate-limited", key });
  }

  public accept(
    operation: string,
    key: string,
    options: { readonly deduplicate: boolean; readonly hidden: boolean; readonly authorized?: boolean },
  ): string {
    appendLine(this.sendsPath, { operation, key, authorized: (options.authorized ?? true) ? "True" : "False" });
    if (options.deduplicate) {
      const existing = this.writes(operation).find((entry) => entry.key === key);
      if (existing?.id !== undefined) {
        return existing.id;
      }
    }
    const id = `${operation}-${this.writes().length + 1}`;
    appendLine(this.writesPath, { operation, key, id, hidden: options.hidden ? "True" : "False" });
    return id;
  }

  public find(operation: string, key: string): string | undefined {
    return this.writes(operation).find((entry) => entry.key === key && entry.hidden !== "True")?.id;
  }

  public async waitAt(words: readonly string[], prefix: string, attempt: number): Promise<void> {
    for (const word of words) {
      if (word.startsWith(`${prefix}:`) && attempt === 1) {
        const gate = new FileGate(this.stateDirectory, word.slice(prefix.length + 1));
        gate.markReached();
        await gate.waitForRelease();
      }
    }
  }
}

export class GetBalanceOperation implements Query<GetBalanceInput, Balance> {
  public constructor(private readonly provider: FakeLedgerProvider) {}

  public definition(): QueryDefinition {
    return getBalanceDefinition;
  }

  public async invoke(call: Call, input: GetBalanceInput): Promise<QueryAttempt<Balance>> {
    if (!(await this.provider.authorize(call))) {
      return newQueryBranch("notFound", { accountId: input.accountId, amount: 0 }, {}, refused("getBalance"));
    }
    if (input.accountId === "missing") {
      return newQueryBranch("notFound", { accountId: input.accountId, amount: 0 }, {}, failure("getBalance", "no account"));
    }
    if (input.accountId === "garbled") {
      return newQueryBranch("invalidResponse", { accountId: input.accountId, amount: 0 }, {}, failure("getBalance", "bad body"));
    }
    if (input.accountId === "defect") {
      // The Connector SDK turns an undeclared branch into defect.
      return newQueryBranch("undeclared", { accountId: input.accountId, amount: 0 });
    }
    const payouts = this.provider.writes("createPayout").length;
    const padding = input.padding ?? 0;
    return newQueryBranch("found", {
      accountId: input.accountId,
      amount: 100 - payouts,
      ...(padding > 0 ? { filler: "x".repeat(padding) } : {}),
    });
  }
}

export class CreatePayoutOperation implements Mutation<CreatePayoutInput, Payout> {
  public constructor(private readonly provider: FakeLedgerProvider) {}

  public definition(): QueryDefinition {
    return createPayoutDefinition;
  }

  public idempotencyKey(callId: string): string {
    return callId;
  }

  public async invoke(call: Call, input: CreatePayoutInput): Promise<MutationAttempt<Payout>> {
    const words = (input.memo ?? "").split(/\s+/).filter((word) => word !== "");
    const attempt = call.context.attempt;
    const key = call.idempotencyKey ?? call.id;
    if (!(await this.provider.authorize(call))) {
      return newMutationBranch("providerRejected", {}, {}, refused("createPayout"));
    }
    if (words.includes("reject")) {
      return newMutationBranch("providerRejected", {}, {}, failure("createPayout", "insufficient funds"));
    }
    await this.provider.waitAt(words, "gate-before-send", attempt);
    if (words.includes("rate-limit-once") && attempt === 1) {
      this.provider.recordRateLimited(key);
      return newMutationRetry({ kind: "RATE_LIMIT", provider: fakeLedgerConnectorId, operation: "createPayout", message: "rate limited" });
    }
    const payoutId = this.provider.accept("createPayout", key, { deduplicate: true, hidden: false });
    await this.provider.waitAt(words, "gate-after-accept", attempt);
    if (words.includes("invalid")) {
      return newMutationBranch("invalidResponse", {}, {}, failure("createPayout", "bad body"));
    }
    if (words.includes("defect")) {
      // After the provider accepted the payout, the SDK turns an undeclared branch into defect.
      return newMutationBranch("undeclared", {});
    }
    if (words.includes("uncertain")) {
      return newMutationUncertain({}, failure("createPayout", "server error after send"));
    }
    return newMutationBranch("paid", { payoutId, amount: input.amount }, { providerObjectId: payoutId });
  }
}

export class PostNoteOperation implements Mutation<PostNoteInput, Note> {
  public constructor(private readonly provider: FakeLedgerProvider) {}

  public definition(): QueryDefinition {
    return postNoteDefinition;
  }

  public idempotencyKey(callId: string): string {
    return callId;
  }

  public async invoke(call: Call, input: PostNoteInput): Promise<MutationAttempt<Note>> {
    const words = input.text.split(/\s+/).filter((word) => word !== "");
    const key = call.idempotencyKey ?? call.id;
    if (!(await this.provider.authorize(call))) {
      return newMutationBranch("providerRejected", {}, {}, refused("postNote"));
    }
    if (words.includes("reject")) {
      return newMutationBranch("providerRejected", {}, {}, failure("postNote", "channel archived"));
    }
    const fence = new Fence(call, 30_000);
    const decision = fence.decide();
    const attempt = call.context.attempt;
    if (decision.kind === "failClosed") {
      return newMutationUncertain({}, failure("postNote", decision.error?.message ?? "invalid checkpoint"));
    }
    if (decision.kind === "readBackRequired") {
      const found = this.provider.find("postNote", key);
      if (found === undefined) {
        return newMutationUncertain({}, failure("postNote", "read-back found no note"));
      }
      return newMutationBranch("posted", { noteId: found }, { providerObjectId: found });
    }
    await this.provider.waitAt(words, "gate-before-send", attempt);
    if (words.includes("rate-limit-once") && attempt === 1) {
      await fence.recordNotSent();
      this.provider.recordRateLimited(key);
      return newMutationRetry({ kind: "RATE_LIMIT", provider: fakeLedgerConnectorId, operation: "postNote", message: "rate limited" });
    }
    const noteId = await fence.dispatch(async (): Promise<string | undefined> => {
      await this.provider.waitAt(words, "gate-after-claim", attempt);
      if (words.includes("rate-limit-after-send") && attempt === 1) {
        // The request reached the provider, which refused it without acting.
        this.provider.recordRateLimited(key);
        return undefined;
      }
      const id = this.provider.accept("postNote", key, { deduplicate: false, hidden: words.includes("hide") });
      await this.provider.waitAt(words, "gate-after-accept", attempt);
      return id;
    });
    if (noteId === undefined) {
      await this.provider.waitAt(words, "gate-before-can-resend", attempt);
      await fence.recordCanResend();
      return newMutationRetry({ kind: "RATE_LIMIT", provider: fakeLedgerConnectorId, operation: "postNote", message: "rate limited" });
    }
    await fence.recordProviderId(noteId);
    if (words.includes("uncertain")) {
      return newMutationUncertain({}, failure("postNote", "no response"));
    }
    return newMutationBranch("posted", { noteId }, { providerObjectId: noteId });
  }
}

/** A Mutation no review exposed to agents. */
export class CloseAccountOperation implements Mutation<CloseAccountInput, Note> {
  public definition(): QueryDefinition {
    return closeAccountDefinition;
  }

  public idempotencyKey(callId: string): string {
    return callId;
  }

  public invoke(): MutationAttempt<Note> {
    throw new Error("closeAccount must never run");
  }
}

function refused(operation: string): Failure {
  return { kind: "AUTHENTICATION", provider: fakeLedgerConnectorId, operation, message: "the ledger refused the credential" };
}

function failure(operation: string, message: string): Failure {
  return { kind: "PROVIDER_REJECTION", provider: fakeLedgerConnectorId, operation, message };
}

function readLines(path: string): ProviderEntry[] {
  if (!existsSync(path)) {
    return [];
  }
  return readFileSync(path, "utf8")
    .split("\n")
    .filter((line) => line !== "")
    .map((line) => JSON.parse(line) as ProviderEntry);
}

function appendLine(path: string, entry: ProviderEntry): void {
  appendFileSync(path, `${JSON.stringify(entry)}\n`);
  const descriptor = openSync(path, "r");
  try {
    fsyncSync(descriptor);
  } finally {
    closeSync(descriptor);
  }
}
