// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * OpenTelemetry GenAI semantic conventions version that the session's spans follow.
 *
 * The GenAI conventions are still marked Development, so attribute names can change between versions.
 * The session pins one version and sets its schema URL on the tracer. Pick an older version when a
 * tracing backend still reads the older names.
 *
 * - `"1.42.0-dev"`: the conventions in `open-telemetry/semantic-conventions-genai` at commit
 *   `dc3571562603552dc555c667dfa327a638d94ab2` (2026-11-01), whose schema URL names `1.42.0-dev`. That repository has no release tag
 *   yet. The default. Chat spans carry `gen_ai.provider.name` and the cache and reasoning token counts, and
 *   content capture is available.
 * - `"1.36.0"`: OpenTelemetry semantic conventions 1.36.0, the last release before `gen_ai.system`
 *   became `gen_ai.provider.name`. Chat spans carry `gen_ai.system` and only the input and output token
 *   counts. Content capture is not available, because 1.36.0 recorded content as events.
 */
export type GenAISemanticConventionsVersion = "1.42.0-dev" | "1.36.0";

/**
 * OpenTelemetry schema URL of each {@link GenAISemanticConventionsVersion}, set on the session's tracer.
 *
 * @example
 * ```ts
 * const schemaUrl = genAISemanticConventionsSchemaUrls["1.42.0-dev"];
 * ```
 */
export const genAISemanticConventionsSchemaUrls: Readonly<Record<GenAISemanticConventionsVersion, string>> =
  Object.freeze({
    "1.42.0-dev": "https://opentelemetry.io/schemas/gen-ai-dev/1.42.0-dev",
    "1.36.0": "https://opentelemetry.io/schemas/1.36.0",
  });

/**
 * The part of an OpenTelemetry `TracerProvider` the session uses.
 *
 * Any `TracerProvider` from `@opentelemetry/api`, such as an SDK `BasicTracerProvider` or
 * `NodeTracerProvider`, satisfies it. The core declares it structurally so that applications without
 * `@opentelemetry/api` still type-check.
 */
export interface OpenTelemetryTracerProvider {
  /**
   * Returns a tracer, as `TracerProvider.getTracer` does.
   * @param name - Instrumentation scope name.
   * @param version - Instrumentation scope version.
   * @param options - Tracer options; the session passes the schema URL.
   * @returns An OpenTelemetry `Tracer`.
   */
  getTracer(name: string, version?: string, options?: { readonly schemaUrl?: string }): unknown;
}

/**
 * OpenTelemetry GenAI span options of one agent session Flow, passed as
 * {@link AgentSessionOptions.tracing}.
 *
 * Tracing is on by default and uses the global tracer provider, so an application that registers the
 * OpenTelemetry SDK gets spans without further setup. `@opentelemetry/api` is an optional peer
 * dependency: when it is not installed, or no SDK is registered, the session emits nothing and stores
 * nothing extra.
 *
 * The session emits one trace per activation: a burst of Steps that starts when the session consumes
 * a user message or an interrupt answer, and ends when the session waits again. The activation's root
 * span is `invoke_agent {agent name}`. Each `RunModel` attempt adds a `chat` span, named
 * `chat {model}` when the runner returns `modelCallMetadata`, and each tool Step attempt adds an
 * `execute_tool {tool name}` span. Every span carries the Flow ID as `gen_ai.conversation.id` and
 * `session.id`. Tracing never fails a Step: an error from OpenTelemetry, such as a throwing span
 * processor, is logged with `console.warn` and the span is skipped. The package README lists every
 * span and attribute.
 *
 * @example
 * ```ts
 * import { BasicTracerProvider, SimpleSpanProcessor, ConsoleSpanExporter } from "@opentelemetry/sdk-trace-base";
 *
 * const tracerProvider = new BasicTracerProvider({
 *   spanProcessors: [new SimpleSpanProcessor(new ConsoleSpanExporter())],
 * });
 * const flow = createAgentSessionFlow(runner, "TriageAgent", {
 *   tracing: { tracerProvider, agentName: "Triage", shouldCaptureMessageContent: true },
 * });
 * ```
 */
export interface AgentTracingOptions {
  /** Whether the session emits spans. Defaults to `true`. `false` never loads `@opentelemetry/api`. */
  readonly isEnabled?: boolean;
  /**
   * Tracer provider to emit with. Defaults to the global provider, read at the first Step that emits a
   * span.
   */
  readonly tracerProvider?: OpenTelemetryTracerProvider;
  /** `gen_ai.agent.name` and the `invoke_agent` span name. Defaults to the Flow type. */
  readonly agentName?: string;
  /**
   * Whether spans carry message text, tool arguments, and tool results. Defaults to `false`: spans then
   * carry only Dex references, such as `AgentMessages` instance keys and tool call IDs, because the
   * content is already durable in Dex. Content can be sensitive and large; enable it only where the
   * tracing backend may store it.
   */
  readonly shouldCaptureMessageContent?: boolean;
  /** GenAI semantic conventions version. Defaults to `"1.42.0-dev"`. */
  readonly semanticConventionsVersion?: GenAISemanticConventionsVersion;
}

/** Tracing options applied by an agent session Flow, with every default filled in. */
export interface ResolvedAgentTracingOptions {
  /** Whether the session emits spans. */
  readonly isEnabled: boolean;
  /** Tracer provider, or `undefined` for the global provider. */
  readonly tracerProvider: OpenTelemetryTracerProvider | undefined;
  /** `gen_ai.agent.name`: the configured name or the Flow type. */
  readonly agentName: string;
  /** Whether spans carry message content. */
  readonly shouldCaptureMessageContent: boolean;
  /** GenAI semantic conventions version. */
  readonly semanticConventionsVersion: GenAISemanticConventionsVersion;
}
