'use client';

import StatusBadge from '../../components/StatusBadge';
import type { ActiveStepExecutionLive, ChannelMessagesEntry, RunDetail } from '../../api/_grpc/mappers';

// RunStatePanel is the left-rail companion to the graph/timeline. It
// surfaces the LIVE view of a run as fetched from RunsService.GetRun: the
// authoritative status (so we no longer infer it from RunStop history
// events), the current state map, and any unconsumed channel messages
// pending consumption by waiting steps.
//
// The panel is intentionally read-only and self-contained — the parent
// page owns the fetch + auto-poll loop and pushes results in via props.
// Every field re-renders on every poll tick so "fired 3s ago" / "Live"
// indicators stay fresh without per-component intervals.
export default function RunStatePanel({
  detail,
  loading,
  error,
  lastFetchedAt,
  polling,
}: {
  detail: RunDetail | null;
  loading: boolean;
  error: string | null;
  lastFetchedAt: number | null;
  polling: boolean;
}) {
  return (
    <div
      data-testid="run-state-panel"
      className="bg-white shadow-sm border border-gray-200 rounded-lg p-4 space-y-4 sticky top-4"
    >
      <Header
        detail={detail}
        loading={loading}
        error={error}
        lastFetchedAt={lastFetchedAt}
        polling={polling}
      />
      {detail && (
        <>
          <StateSection state={detail.state} />
          <ActiveStepsSection activeSteps={detail.activeStepExecutions} />
          <ChannelsSection channels={detail.unconsumedChannelMessages} />
          <FooterSection detail={detail} />
        </>
      )}
    </div>
  );
}

function Header({
  detail,
  loading,
  error,
  lastFetchedAt,
  polling,
}: {
  detail: RunDetail | null;
  loading: boolean;
  error: string | null;
  lastFetchedAt: number | null;
  polling: boolean;
}) {
  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <div className="text-xs uppercase tracking-wide text-gray-500">Live state</div>
        {polling && detail && !isTerminal(detail.status) && (
          <span
            data-testid="run-state-live-indicator"
            className="inline-flex items-center gap-1 text-[10px] text-green-700"
            title="Auto-refreshing every 2s"
          >
            <span className="w-1.5 h-1.5 rounded-full bg-green-500 animate-pulse" />
            Live
          </span>
        )}
      </div>
      <div className="flex items-center gap-2 mb-1">
        {detail ? (
          <StatusBadge status={detail.status} />
        ) : loading ? (
          <span className="text-xs text-gray-500">Loading…</span>
        ) : error ? (
          <span className="text-xs text-red-700">Error</span>
        ) : (
          <span className="text-xs text-gray-500">No data</span>
        )}
      </div>
      {lastFetchedAt && (
        <div className="text-[10px] text-gray-400">
          Updated {relativeTime(lastFetchedAt)}
        </div>
      )}
      {error && (
        <div className="mt-1 text-xs text-red-700 break-words" data-testid="run-state-error">
          {error}
        </div>
      )}
    </div>
  );
}

function StateSection({ state }: { state: Record<string, unknown> }) {
  const entries = Object.entries(state);
  return (
    <section>
      <div className="text-xs font-medium uppercase tracking-wide text-gray-500 mb-2">
        State
      </div>
      {entries.length === 0 ? (
        <div className="text-xs text-gray-400">empty</div>
      ) : (
        // Stacked layout: key on its own line, value below with full panel
        // width. Variable-length values (long strings, objects, arrays) need
        // the room — putting them inline next to the key cramps both.
        <dl className="space-y-2.5">
          {entries.map(([k, v]) => (
            <div key={k}>
              <dt className="font-mono text-[11px] font-medium text-gray-700 break-all mb-0.5">
                {k}
              </dt>
              <dd className="text-xs text-gray-900 pl-2 border-l-2 border-gray-100">
                <ValueDisplay value={v} />
              </dd>
            </div>
          ))}
        </dl>
      )}
    </section>
  );
}

function ChannelsSection({ channels }: { channels: ChannelMessagesEntry[] }) {
  return (
    <section>
      <div className="text-xs font-medium uppercase tracking-wide text-gray-500 mb-2">
        Unconsumed channel messages
      </div>
      {channels.length === 0 ? (
        <div className="text-xs text-gray-400">none pending</div>
      ) : (
        // Same stacked rhythm as StateSection: channel name + count on top,
        // values below. Each value gets its own line so JSON objects can
        // render as readable pre blocks instead of being collapsed away.
        <ul className="space-y-3">
          {channels.map((ch) => (
            <li key={ch.channelName}>
              <div className="flex items-baseline justify-between mb-1 gap-2">
                <code className="font-mono text-[11px] font-medium text-gray-700 break-all">
                  {ch.channelName}
                </code>
                <span className="text-[10px] text-gray-500 shrink-0">
                  {ch.values.length} pending
                </span>
              </div>
              <ol className="space-y-1 pl-2 border-l-2 border-gray-100">
                {ch.values.map((v, i) => (
                  <li key={i} className="text-xs text-gray-900">
                    <ValueDisplay value={v} />
                  </li>
                ))}
              </ol>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function FooterSection({ detail }: { detail: RunDetail }) {
  return (
    <section className="border-t border-gray-100 pt-2">
      <dl className="grid grid-cols-2 gap-x-3 gap-y-1 text-[10px] text-gray-500">
        <Footer label="Version" value={String(detail.version)} />
        <Footer label="Worker req #" value={String(detail.workerRequestCounter)} />
        <Footer label="Ext channel #" value={String(detail.externalChannelMessageCounter)} />
        <Footer label="Durable timer" value={detail.durableTimerFired ? 'fired' : '—'} />
      </dl>
    </section>
  );
}

function Footer({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className="truncate">{label}</dt>
      <dd className="font-mono text-gray-700 truncate">{value}</dd>
    </>
  );
}

// ValueDisplay renders one dex-flattened value. Primitives go inline as
// mono text (no quote noise — context already says "this is a value");
// objects/arrays render as a full-width pretty-printed pre block since the
// parent gives each value its own row. Blob/encoded refs collapse to a muted
// hint to keep the panel from blowing up on large payloads.
function ValueDisplay({ value }: { value: unknown }) {
  if (value === null) return <span className="text-gray-400 italic">null</span>;
  if (value === undefined) return <span className="text-gray-400 italic">undefined</span>;
  if (typeof value === 'string') {
    // No surrounding quotes: the dt label + indent already mark the boundary,
    // and quotes around long strings only add visual noise + wrap weirdness.
    return <span className="font-mono text-xs text-gray-900 break-words whitespace-pre-wrap">{value}</span>;
  }
  if (typeof value === 'number' || typeof value === 'bigint' || typeof value === 'boolean') {
    return <span className="font-mono text-xs text-blue-700">{String(value)}</span>;
  }
  if (typeof value !== 'object') {
    return <span className="font-mono text-xs text-gray-900">{String(value)}</span>;
  }
  const obj = value as Record<string, unknown>;
  if (obj.__blobRef) {
    const id = String(obj.blob_id ?? '?');
    const short = id.length > 12 ? id.slice(0, 12) + '…' : id;
    return (
      <span className="text-xs text-gray-500" title={id}>
        [blob: {short}]
      </span>
    );
  }
  if (obj.__encoded) {
    return (
      <span className="text-xs text-gray-500">[{String(obj.encoding)} payload]</span>
    );
  }
  return (
    <pre className="font-mono text-[11px] text-gray-900 bg-gray-50 border border-gray-200 rounded p-1.5 overflow-x-auto whitespace-pre-wrap break-all">
      {JSON.stringify(value, null, 2)}
    </pre>
  );
}

const TERMINAL_STATUSES = new Set([4, 5]); // Completed, Failed

function ActiveStepsSection({
  activeSteps,
}: {
  activeSteps: Record<string, ActiveStepExecutionLive>;
}) {
  const entries = Object.entries(activeSteps);
  return (
    <section data-testid="active-step-retry">
      <div className="text-xs font-medium uppercase tracking-wide text-gray-500 mb-2">
        Active steps
      </div>
      {entries.length === 0 ? (
        <div className="text-xs text-gray-400">none</div>
      ) : (
        <ul className="space-y-2">
          {entries.map(([stepExeId, live]) => {
            const retry = live.executeRetryState ?? live.waitForRetryState;
            return (
              <li key={stepExeId} className="text-xs border border-gray-200 rounded p-2 bg-gray-50">
                <div className="font-mono break-all">{stepExeId}</div>
                <div className="text-gray-600 mt-1">status: {live.status}</div>
                {retry && (
                  <div className="text-amber-800 mt-1" data-testid="step-retry-panel">
                    attempt {retry.currentAttempts}
                    {retry.lastError != null && (
                      <div className="mt-1 break-all">error: {retry.lastError}</div>
                    )}
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

function isTerminal(status: number): boolean {
  return TERMINAL_STATUSES.has(status);
}

function relativeTime(ms: number): string {
  const delta = Date.now() - ms;
  if (delta < 1000) return 'just now';
  const s = Math.floor(delta / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  return `${Math.floor(m / 60)}h ago`;
}
