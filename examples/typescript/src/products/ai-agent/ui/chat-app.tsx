/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import { useChat } from "@ai-sdk/react";
import { DefaultChatTransport, lastAssistantMessageIsCompleteWithApprovalResponses, type UIMessage } from "ai";
import { useCallback, useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";

interface StoredChat {
  readonly messages: UIMessage[];
  readonly resumeToken: string | null;
}

interface AgentSessionAccess {
  readonly sessionId: string;
  readonly sessionToken: string;
}

interface PendingMessage {
  readonly message_id: string;
  readonly message: { readonly text: string; readonly plan_mode?: boolean | null };
}

interface PendingQuestion {
  readonly interrupt_id: string;
  readonly kind: string;
  readonly question: string | null;
  readonly choices: readonly string[] | null;
  readonly allows_free_text: boolean | null;
}

interface SessionSnapshot {
  readonly state: { readonly status: string; readonly active_turn_id: string | null } | null;
  readonly pending_interrupts: readonly PendingQuestion[];
  readonly plan: {
    readonly revision: number;
    readonly status: string;
    readonly tasks: readonly { readonly content: string; readonly status: string }[];
  } | null;
  readonly queued_messages: readonly PendingMessage[] | null;
  readonly steered_messages: readonly PendingMessage[] | null;
  readonly context_summary: { readonly last_sequence: number } | null;
  readonly usage: { readonly model_calls: number };
  readonly session_settings?: { readonly model?: string } | null;
}

async function callJson<T>(path: string, sessionToken: string, method = "GET", body?: unknown): Promise<T> {
  const headers = { "content-type": "application/json", "X-Agent-Session-Token": sessionToken };
  const response = await fetch(path, { method, headers, ...(body === undefined ? {} : { body: JSON.stringify(body) }) });
  if (!response.ok) {
    throw new Error(await response.text());
  }
  return (await response.json()) as T;
}

const sessionTokenKey = (sessionId: string) => `ai-agent-session-token:${sessionId}`;

// The token stays in this browser's storage, so a copied URL carries only the session ID.
function readSessionToken(sessionId: string): string {
  try {
    return localStorage.getItem(sessionTokenKey(sessionId)) ?? "";
  } catch {
    return "";
  }
}

function saveSessionToken(sessionId: string, sessionToken: string): void {
  try {
    localStorage.setItem(sessionTokenKey(sessionId), sessionToken);
  } catch {
    // Without storage the session works until the page reloads.
  }
}

async function startSession(model: string): Promise<void> {
  const started = await callJson<{ session_id: string; session_token: string }>("sessions", "", "POST", { model });
  saveSessionToken(started.session_id, started.session_token);
  location.hash = new URLSearchParams({ session: started.session_id }).toString();
}

function startNewSession(): void {
  location.hash = "";
  location.reload();
}

function App() {
  const [session, setSession] = useState<{ access: AgentSessionAccess; chat: StoredChat }>();
  const [error, setError] = useState<string>();
  const [sessionId, setSessionId] = useState(() => new URLSearchParams(location.hash.slice(1)).get("session"));
  useEffect(() => {
    const followHash = () => setSessionId(new URLSearchParams(location.hash.slice(1)).get("session"));
    addEventListener("hashchange", followHash);
    return () => removeEventListener("hashchange", followHash);
  }, []);
  useEffect(() => {
    if (sessionId === null) return;
    const access = { sessionId, sessionToken: readSessionToken(sessionId) };
    callJson<StoredChat>(`api/chat/${sessionId}`, access.sessionToken)
      .then((chat) => setSession({ access, chat }))
      .catch((failure: unknown) => setError(String(failure)));
  }, [sessionId]);
  if (error !== undefined) {
    return <p className="error">{error} <button onClick={startNewSession}>Start a new session</button></p>;
  }
  if (sessionId === null) return <StartForm onError={setError} />;
  return session === undefined ? <p>Opening the session…</p> : <AgentChat access={session.access} initial={session.chat} />;
}

// Each session picks one of the models the Worker allows; its settings carry only that name.
function StartForm({ onError }: { onError: (message: string) => void }) {
  const [models, setModels] = useState<string[]>([]);
  const [model, setModel] = useState("");
  useEffect(() => {
    callJson<{ model: string; models: string[] }>("settings", "")
      .then((settings) => {
        setModels(settings.models);
        setModel(settings.model);
      })
      .catch((failure: unknown) => onError(String(failure)));
  }, [onError]);
  return (
    <main>
      <header><b>Dex AI Agent</b></header>
      <label>
        Model{" "}
        <select value={model} onChange={(event) => setModel(event.target.value)}>
          {models.map((name) => <option key={name} value={name}>{name}</option>)}
        </select>
      </label>
      <button disabled={model === ""} onClick={() => void startSession(model).catch((failure: unknown) => onError(String(failure)))}>
        Start session
      </button>
    </main>
  );
}

function AgentChat({ access, initial }: { access: AgentSessionAccess; initial: StoredChat }) {
  const { sessionId, sessionToken } = access;
  const resumeToken = useRef(initial.resumeToken ?? undefined);
  const [snapshot, setSnapshot] = useState<SessionSnapshot>();
  const [notice, setNotice] = useState<string>();
  const [draft, setDraft] = useState("");
  const [isPlanMode, setPlanMode] = useState(false);
  const chat = useChat({
    id: sessionId,
    messages: initial.messages,
    resume: initial.resumeToken !== null,
    onData: (part) => {
      if (part.type === "data-agent-resume-token") resumeToken.current = part.data as string;
    },
    // "tool-calls": the turn waits for approvals, which useChat sends; otherwise follow the next turn.
    onFinish: ({ finishReason }) => {
      if (finishReason !== "tool-calls") void reloadChat();
    },
    onError: (failure) => setNotice(failure.message),
    transport: new DefaultChatTransport({
      api: "api/chat",
      headers: { "X-Agent-Session-Token": sessionToken },
      // Dex owns the conversation, so the route needs only the newest message.
      prepareSendMessagesRequest: ({ id, messages }) => ({ body: { id, messages: messages.slice(-1) } }),
      prepareReconnectToStreamRequest: ({ id }) => ({
        api: `api/chat/${id}/stream`,
        headers: {
          "X-Agent-Session-Token": sessionToken,
          ...(resumeToken.current === undefined ? {} : { "x-agent-resume-token": resumeToken.current }),
        },
      }),
    }),
    sendAutomaticallyWhen: lastAssistantMessageIsCompleteWithApprovalResponses,
  });
  const isStreaming = chat.status === "streaming" || chat.status === "submitted";

  const reloadSnapshot = useCallback(async () => {
    try {
      setSnapshot(await callJson<SessionSnapshot>(`sessions/${sessionId}/snapshot`, sessionToken));
    } catch (failure) {
      setNotice(String(failure));
    }
  }, [sessionId, sessionToken]);

  // After a turn ends or a side action, show the stored messages and follow the active turn.
  const reloadChat = useCallback(async () => {
    const stored = await callJson<StoredChat>(`api/chat/${sessionId}`, sessionToken);
    chat.setMessages(stored.messages);
    await reloadSnapshot();
    if (stored.resumeToken !== null) {
      resumeToken.current = stored.resumeToken;
      await chat.resumeStream();
    }
  }, [sessionId, sessionToken, chat, reloadSnapshot]);

  useEffect(() => {
    void reloadSnapshot();
    const timer = setInterval(() => void reloadSnapshot(), 2_000);
    return () => clearInterval(timer);
  }, [reloadSnapshot, chat.status]);

  const act = async (path: string, method: string, body?: unknown) => {
    try {
      setNotice(undefined);
      await callJson(`sessions/${sessionId}/${path}`, sessionToken, method, body);
      if (!isStreaming) await reloadChat();
      else await reloadSnapshot();
    } catch (failure) {
      setNotice(String(failure));
    }
  };

  const send = async () => {
    const text = draft.trim();
    setDraft("");
    const isIdle = snapshot?.state?.active_turn_id == null && !isStreaming;
    if (isIdle && !isPlanMode) await chat.sendMessage({ text });
    else await act("messages", "POST", { text, plan_mode: isPlanMode });
  };

  const questions = snapshot?.pending_interrupts.filter((interrupt) => interrupt.kind === "question") ?? [];
  const plan = snapshot?.plan;
  return (
    <main>
      <header>
        <b>Dex AI Agent</b> · {snapshot?.session_settings?.model ?? "default model"} · {snapshot?.state?.status ?? "…"} ·{" "}
        {snapshot?.usage.model_calls ?? 0} model calls
        {snapshot?.context_summary != null && <> · summarized through message {snapshot.context_summary.last_sequence}</>}
        <button onClick={() => void act("cancel", "POST", {})}>Stop turn</button>
        <button onClick={startNewSession}>New session</button>
      </header>
      {notice !== undefined && <p className="error">{notice}</p>}
      {chat.messages.map((message) => (
        <section key={message.id} className={message.role}>
          {message.parts.map((part, index) => (
            <MessagePart key={index} part={part} onApprove={chat.addToolApprovalResponse} />
          ))}
        </section>
      ))}
      {questions.map((question) => (
        <QuestionPanel key={question.interrupt_id} question={question} onAction={act} />
      ))}
      {plan != null && plan.tasks.length > 0 && (
        <section className="panel">
          <p>Plan revision {plan.revision} ({plan.status})</p>
          <ul>{plan.tasks.map((task) => <li key={task.content}>[{task.status}] {task.content}</li>)}</ul>
          {(plan.status === "draft" || plan.status === "active") && !isStreaming && (
            <button onClick={() => void act("plan/execute", "POST", { revision: plan.revision })}>
              {plan.status === "draft" ? "Execute plan" : "Continue plan"}
            </button>
          )}
        </section>
      )}
      {[...(snapshot?.queued_messages ?? []), ...(snapshot?.steered_messages ?? [])].map((pending) => (
        <section key={pending.message_id} className="panel">
          Queued: {pending.message.text}
          <button onClick={() => void act("steer", "POST", { message_id: pending.message_id })}>Steer</button>
          <button onClick={() => void act(`queued-messages/${pending.message_id}`, "DELETE")}>Delete</button>
        </section>
      ))}
      <footer>
        <textarea value={draft} onChange={(event) => setDraft(event.target.value)} placeholder="Try /choose Pick | a | b, /wait 5 a pause, /tool notes__search_notes {&quot;query&quot;:&quot;Step&quot;}" />
        <label><input type="checkbox" checked={isPlanMode} onChange={(event) => setPlanMode(event.target.checked)} /> Plan mode</label>
        <button disabled={draft.trim() === ""} onClick={() => void send()}>{isStreaming ? "Queue" : "Send"}</button>
        <button disabled={draft.trim() === "" || !isStreaming} onClick={() => void act("steer", "POST", { text: draft }).then(() => setDraft(""))}>
          Steer
        </button>
      </footer>
    </main>
  );
}

type SessionAction = (path: string, method: string, body?: unknown) => Promise<void>;

function QuestionPanel({ question, onAction }: { question: PendingQuestion; onAction: SessionAction }) {
  const [answer, setAnswer] = useState("");
  const answerPath = `questions/${question.interrupt_id}/answer`;
  const choices = question.choices ?? [];
  return (
    <section className="panel">
      <p>{question.question}</p>
      {choices.map((choice) => (
        <button key={choice} onClick={() => void onAction(answerPath, "POST", { answer: choice })}>{choice}</button>
      ))}
      {(choices.length === 0 || question.allows_free_text !== false) && (
        <>
          <input value={answer} onChange={(event) => setAnswer(event.target.value)} placeholder="Your answer" />
          <button disabled={answer.trim() === ""} onClick={() => void onAction(answerPath, "POST", { answer })}>Answer</button>
        </>
      )}
      <button onClick={() => void onAction(`questions/${question.interrupt_id}/decline`, "POST", {})}>Decline</button>
    </section>
  );
}

type ApproveTool = ReturnType<typeof useChat>["addToolApprovalResponse"];

function MessagePart({ part, onApprove }: { part: UIMessage["parts"][number]; onApprove: ApproveTool }) {
  if (part.type === "text") return <p>{part.text}</p>;
  if (part.type === "reasoning") return <p className="reasoning">Thinking: {part.text}</p>;
  if (part.type !== "dynamic-tool") return null;
  const output = "output" in part ? JSON.stringify(part.output) : "errorText" in part ? part.errorText : "";
  return (
    <div className="tool">
      <code>{part.toolName}({JSON.stringify(part.input)})</code> {part.state} {output}
      {part.state === "approval-requested" && (
        <>
          <button onClick={() => void onApprove({ id: part.approval.id, approved: true })}>Approve</button>
          <button onClick={() => void onApprove({ id: part.approval.id, approved: false, reason: "The user rejected it." })}>Reject</button>
        </>
      )}
    </div>
  );
}

createRoot(document.getElementById("root")!).render(<App />);
