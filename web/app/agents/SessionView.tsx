// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useRef, useState, type ReactNode } from 'react';
import {
  Bot,
  CircleAlert,
  CircleDashed,
  Gauge,
  History,
  Info,
  Layers,
  MessageSquare,
  RotateCw,
  ShieldAlert,
  Sparkles,
  UserRound,
  Wrench,
  type LucideIcon,
} from 'lucide-react';
import { useWebCatalog } from '../catalog/WebCatalogProvider';
import { isOpenFlowStatusCode } from '../work-queue/liveness';
import type { SettledOutcome } from './ApprovalCard';
import { SESSION_COPY } from './copy';
import { InterruptControls, useSessionActionInvoker } from './InterruptControls';
import type { SessionControlContext } from './sessionActions';
import { SessionComposer } from './SessionComposer';
import type { ConversationItem, ToolCallView } from './sessionModel';
import { sessionActionPermission, sessionRouteBlockedReason, type SessionRouteContext } from './sessionRequests';
import { SESSION_RPC, type AgentSessionSchema } from './sessionSchema';
import { SessionSettingsPanel } from './SessionSettingsPanel';
import { ToolCallCard } from './ToolCallCard';
import { useAgentSession, type LoadedAgentSession } from './useAgentSession';
import './session.css';

/** The Session tab: one conversation with each tool call inline, and Steer and Stop below. */
export function SessionView({
  flowType,
  flowId,
  schema,
  reloadKey,
  workQueuePermissions,
  onChooseWorkingAs,
}: {
  flowType: string;
  flowId: string;
  schema: AgentSessionSchema;
  reloadKey: number;
  workQueuePermissions: readonly string[];
  /** Sets Working as, so a phone, which hides the run list, can still act. */
  onChooseWorkingAs?: (permission: string) => void;
}) {
  const invoke = useSessionActionInvoker(flowType, flowId, workQueuePermissions);
  const { revisionFor } = useWebCatalog();
  const { held, reload, includeChunks, loadEarlierMessages } = useAgentSession(flowId, reloadKey);
  const [steerText, setSteerText] = useState('');
  /** Kept outside the card: the reload that follows removes the card's own controls. */
  const [handledElsewhere, setHandledElsewhere] = useState<ReadonlySet<string>>(new Set());
  const steerRef = useRef<HTMLTextAreaElement>(null);

  const session = held.value;
  if (!session) {
    return (
      <section className="ses" aria-label={SESSION_COPY.tabSession}>
        {held.liveness === 'unreachable'
          ? (
            <p className="ses-error" role="alert">
              <CircleAlert aria-hidden="true" size={16} />
              <span>{SESSION_COPY.readFailed(held.reason ?? '')}</span>
            </p>
          )
          : <p className="ses-state">{SESSION_COPY.loading}</p>}
        {held.liveness === 'unreachable' && <ReloadButton onReload={reload} />}
      </section>
    );
  }

  const now = Date.now();
  const controlContext: SessionControlContext = {
    isFlowOpen: isOpenFlowStatusCode(session.summary.flowStatusCode),
    workQueuePermissions,
  };
  const routeContext: SessionRouteContext = {
    flowType,
    flowId,
    workQueuePermissions,
    definitionRevision: revisionFor([flowType]),
  };
  const tellAgent = (call: ToolCallView) => {
    setSteerText(SESSION_COPY.tellAgentPrefix(call.toolName, call.toolCallId));
    steerRef.current?.focus();
  };

  const settle = (toolCallId: string) => (outcome: SettledOutcome) => {
    if (outcome === 'already_handled') setHandledElsewhere((current) => new Set(current).add(toolCallId));
    reload();
  };
  const renderControls = (call: ToolCallView) => (
    <InterruptControls
      call={call}
      controlContext={controlContext}
      flowId={flowId}
      flowType={flowType}
      isHandledElsewhere={handledElsewhere.has(call.toolCallId)}
      now={now}
      schema={schema}
      onSettled={settle(call.toolCallId)}
    />
  );

  const { view } = session;
  const neededPermission = sessionPermissionToChoose(schema, workQueuePermissions);
  const permissionHint = neededPermission && onChooseWorkingAs ? SESSION_COPY.chooseWorkingAs(neededPermission) : null;
  const settingsBlocked = sessionRouteBlockedReason(schema, SESSION_RPC.updateSessionSettings, controlContext);
  // Sending checks this permission, which can differ from the first one Working as lacks.
  const sendPermission = sessionActionPermission(schema);
  return (
    <section className="ses" aria-label={SESSION_COPY.tabSession}>
      <SessionHeader session={session} onReload={reload} />
      {held.liveness === 'stale' && (
        <p className="ses-note" data-kind="stale" role="status">
          <Info aria-hidden="true" size={13} />
          <span>{SESSION_COPY.stale} {held.reason}</span>
        </p>
      )}
      {view.missing.length > 0 && (
        <div className="ses-missing" role="alert">
          <b><CircleAlert aria-hidden="true" size={16} />{SESSION_COPY.missingHeading}</b>
          <ul>{view.missing.map((entry) => <li key={entry}>{entry}</li>)}</ul>
        </div>
      )}
      {neededPermission && onChooseWorkingAs && (
        <p className="ses-permission">
          <ShieldAlert aria-hidden="true" size={15} />
          <span>{permissionHint}</span>
          <button className="ses-workas" onClick={() => onChooseWorkingAs(neededPermission)} type="button">
            {SESSION_COPY.workAs(neededPermission)}
          </button>
        </p>
      )}
      {schema.language === 'go' && <SessionNote>{SESSION_COPY.goReduced}</SessionNote>}
      <SessionSettingsPanel
        blockedReason={settingsBlocked}
        isBlockedReasonShownAbove={settingsBlocked === permissionHint}
        routeContext={routeContext}
        settings={view.sessionSettings}
        onSaved={reload}
      />
      <div className="ses-stream">
        {view.messageIndex && view.messageIndex.firstSequence > 1 && (
          <SessionNote icon={History}>{SESSION_COPY.deletedBefore(view.messageIndex.firstSequence)}</SessionNote>
        )}
        {view.chunkKeys.length > 0 && !includeChunks && (
          <button className="ses-earlier" onClick={loadEarlierMessages} type="button">
            <History aria-hidden="true" size={14} />
            {SESSION_COPY.earlierMessages(view.chunkKeys.length)}
          </button>
        )}
        {session.unreadableChunks > 0 && <SessionNote>{SESSION_COPY.chunkUnreadable(session.unreadableChunks)}</SessionNote>}
        {view.hasCallsFromMessagesOnly && (
          <SessionNote icon={History} kind="history">{SESSION_COPY.pastCallsNote}</SessionNote>
        )}
        {view.items.length === 0 && view.missing.length === 0 && <p className="ses-state">{SESSION_COPY.noMessages}</p>}
        {view.items.map((item, index) => (
          <StreamItem
            item={item}
            key={item.key}
            now={now}
            previousTurnId={turnOf(view.items[index - 1])}
            renderControls={renderControls}
            onTellAgent={sessionActionPermission(schema) !== null ? tellAgent : undefined}
          />
        ))}
        {view.state?.lastTurnError && (
          <p className="ses-error" role="alert">
            <CircleAlert aria-hidden="true" size={16} />
            <span><b>{SESSION_COPY.lastTurnError}</b> {view.state.lastTurnError}</span>
          </p>
        )}
      </div>
      <SessionComposer
        context={controlContext}
        hintShownAbove={permissionHint}
        invoke={invoke}
        workingAsChoice={sendPermission && onChooseWorkingAs
          ? { permission: sendPermission, choose: () => onChooseWorkingAs(sendPermission) }
          : null}
        onActed={reload}
        routeContext={routeContext}
        schema={schema}
        session={session}
        setText={setSteerText}
        text={steerText}
        textRef={steerRef}
      />
    </section>
  );
}

/** The permission of the session's Actions when Working as does not include it yet. */
export function sessionPermissionToChoose(
  schema: AgentSessionSchema,
  workQueuePermissions: readonly string[],
): string | null {
  const actions = [schema.resolveInterrupt, schema.answerQuestion, schema.steerUserMessage, schema.cancelTurn];
  const missing = actions.find((action) => action && !workQueuePermissions.includes(action.requiredPermission));
  return missing?.requiredPermission ?? null;
}

function turnOf(item: ConversationItem | undefined): string | undefined {
  if (!item || item.kind === 'missing') return undefined;
  return item.kind === 'message' ? item.message.turnId : item.call.turnId;
}

/** Each session status as an icon and a tone; running states pulse. */
const SESSION_STATUS_LOOK: Record<string, { icon: LucideIcon; tone: 'idle' | 'active' | 'danger' }> = {
  waiting_for_user_message: { icon: MessageSquare, tone: 'idle' },
  running_model: { icon: Sparkles, tone: 'active' },
  running_tools: { icon: Wrench, tone: 'active' },
  compacting_context: { icon: Layers, tone: 'active' },
  failed: { icon: CircleAlert, tone: 'danger' },
};

function SessionHeader({ session, onReload }: { session: LoadedAgentSession; onReload: () => void }) {
  const { state, usage, waitingCalls } = session.view;
  const look = state ? SESSION_STATUS_LOOK[state.status] ?? { icon: CircleDashed, tone: 'idle' as const } : null;
  const isEveryWaitAQuestion = waitingCalls.length > 0 && waitingCalls.every((call) => call.state === 'awaiting_answer');
  const WaitIcon = isEveryWaitAQuestion ? MessageSquare : ShieldAlert;
  return (
    <header className="ses-head">
      <div className="ses-headline">
        {state && look && (
          <span className="ses-status" data-status={state.status} data-tone={look.tone}>
            <look.icon aria-hidden="true" size={15} strokeWidth={2.2} />
            {SESSION_COPY.sessionStatus[state.status] ?? state.status}
          </span>
        )}
        {waitingCalls.length > 0 && (
          <span className="ses-status" data-tone="attention">
            <WaitIcon aria-hidden="true" size={15} strokeWidth={2.2} />
            {isEveryWaitAQuestion
              ? SESSION_COPY.waitingForAnswer(waitingCalls.length)
              : SESSION_COPY.waitingForApproval(waitingCalls.length)}
          </span>
        )}
        {state?.activeTurnId && <span className="ses-turn">{SESSION_COPY.turn(state.activeTurnId)}</span>}
        <ReloadButton onReload={onReload} />
      </div>
      {usage && (
        <p className="ses-usage" title={SESSION_COPY.usageHeading}>
          <Gauge aria-hidden="true" size={13} />
          <span>
            {SESSION_COPY.usage(usage.inputTokens, usage.outputTokens, usage.cachedInputTokens, usage.reasoningTokens, usage.modelCalls)}
          </span>
        </p>
      )}
    </header>
  );
}

function ReloadButton({ onReload }: { onReload: () => void }) {
  return (
    <button className="ses-reload" onClick={onReload} type="button">
      <RotateCw aria-hidden="true" size={13} strokeWidth={2.2} />
      {SESSION_COPY.reload}
    </button>
  );
}

/** A quiet one-line note: secondary to the conversation, never framed. */
function SessionNote({ children, icon: Icon = Info, kind }: { children: ReactNode; icon?: LucideIcon; kind?: string }) {
  return (
    <p className="ses-note" data-kind={kind}>
      <Icon aria-hidden="true" size={13} />
      <span>{children}</span>
    </p>
  );
}

function StreamItem({
  item,
  previousTurnId,
  now,
  renderControls,
  onTellAgent,
}: {
  item: ConversationItem;
  previousTurnId: string | undefined;
  now: number;
  renderControls: (call: ToolCallView) => ReactNode;
  onTellAgent?: (call: ToolCallView) => void;
}) {
  if (item.kind === 'missing') {
    return (
      <p className="ses-gap" role="note">
        <CircleAlert aria-hidden="true" size={14} />
        <span>{item.label}</span>
      </p>
    );
  }
  const turnId = item.kind === 'message' ? item.message.turnId : item.call.turnId;
  const divider = turnId && turnId !== previousTurnId
    ? <div className="ses-turnmark" role="separator"><span>{SESSION_COPY.turn(turnId)}</span></div>
    : null;
  if (item.kind === 'tool') {
    return (
      <>
        {divider}
        <ToolCallCard call={item.call} now={now} onTellAgent={onTellAgent}>
          {renderControls(item.call)}
        </ToolCallCard>
      </>
    );
  }
  const { message } = item;
  const isUser = message.role === 'user';
  return (
    <>
      {divider}
      <div className="ses-msg" data-role={message.role}>
        <span className="ses-avatar" aria-hidden="true">
          {isUser ? <UserRound size={15} strokeWidth={2} /> : <Bot size={15} strokeWidth={2} />}
        </span>
        <div className="ses-bubble">
          <span
            className="ses-who"
            title={item.stepExecutionId
              ? `${SESSION_COPY.writtenBy} ${item.stepExecutionId}`
              : isUser && message.senderName ? SESSION_COPY.senderNameUnverified : undefined}
          >
            {isUser ? message.senderName ?? SESSION_COPY.user : SESSION_COPY.assistant}
            {isUser && message.senderName && <span className="ses-who-note"> · {SESSION_COPY.senderNameUnverifiedShort}</span>}
          </span>
          <p className="ses-text">{message.text === null ? null : messageTextParts(message.text)}</p>
        </div>
      </div>
    </>
  );
}

const INLINE_MARK = /(\*\*[^*\n]+\*\*|`[^`\n]+`)/;

/** Message text with its bold and inline code marks drawn, as React text so nothing is parsed as HTML. */
export function messageTextParts(text: string): ReactNode[] {
  return text.split(INLINE_MARK).map((part, index) => {
    if (part.length > 4 && part.startsWith('**') && part.endsWith('**')) {
      return <strong key={index}>{messageTextParts(part.slice(2, -2))}</strong>;
    }
    if (part.length > 2 && part.startsWith('`') && part.endsWith('`')) {
      return <code className="ses-code" key={index}>{part.slice(1, -1)}</code>;
    }
    return part;
  });
}

