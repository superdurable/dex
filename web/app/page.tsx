'use client';

import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { Suspense, useCallback, useEffect, useMemo, useState } from 'react';
import Header from './components/Header';
import StatusBadge from './components/StatusBadge';
import { RUN_STATUSES, formatTimestamp } from './components/utils';
import type { RunSummary } from './api/_grpc/mappers';

const PAGE_SIZE = 50;

interface ListResponse {
  runs: RunSummary[];
  nextPageToken: string;
}

function ListInner() {
  const router = useRouter();
  const search = useSearchParams();
  const namespace = search.get('namespace') ?? '';
  const flowType = search.get('flowType') ?? '';
  const statusRaw = search.get('status');
  const status: number | null = statusRaw === null || statusRaw === '' ? null : Number(statusRaw);
  const orderBy = Number(search.get('orderBy') ?? '0');

  const [runs, setRuns] = useState<RunSummary[] | null>(null);
  const [nextPageToken, setNextPageToken] = useState('');
  const [pageStack, setPageStack] = useState<string[]>(['']);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const currentToken = pageStack[pageStack.length - 1];

  const fetchPage = useCallback(
    async (pageToken: string) => {
      if (!namespace) {
        setRuns(null);
        return;
      }
      setLoading(true);
      setError(null);
      try {
        // Omit `status` entirely when "Any status" so the BFF passes
        // proto3 field-presence "unset" through to OpsService.
        const requestBody: Record<string, unknown> = {
          namespace,
          flowType,
          orderBy,
          limit: PAGE_SIZE,
          pageToken,
        };
        if (status !== null) requestBody.status = status;
        const resp = await fetch('/api/runs/list', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(requestBody),
        });
        if (!resp.ok) {
          const errBody = (await resp.json().catch(() => ({}))) as { error?: string };
          throw new Error(errBody.error || `HTTP ${resp.status}`);
        }
        const data = (await resp.json()) as ListResponse;
        setRuns(data.runs ?? []);
        setNextPageToken(data.nextPageToken ?? '');
      } catch (err) {
        setError(err instanceof Error ? err.message : 'unknown error');
        setRuns([]);
      } finally {
        setLoading(false);
      }
    },
    [namespace, flowType, status, orderBy],
  );

  useEffect(() => {
    setPageStack(['']);
    void fetchPage('');
  }, [namespace, flowType, status, orderBy, fetchPage]);

  const onNext = () => {
    if (!nextPageToken) return;
    const newStack = [...pageStack, nextPageToken];
    setPageStack(newStack);
    void fetchPage(nextPageToken);
  };

  const onPrev = () => {
    if (pageStack.length <= 1) return;
    const newStack = pageStack.slice(0, -1);
    setPageStack(newStack);
    void fetchPage(newStack[newStack.length - 1]);
  };

  const updateFilter = (key: string, value: string) => {
    const params = new URLSearchParams(search.toString());
    // For status, '' means "any status" — drop the param. '0' means Pending,
    // which is a real filter and must stay.
    if (value === '' || (key === 'orderBy' && value === '0')) params.delete(key);
    else params.set(key, value);
    router.replace(`/?${params.toString()}`);
  };

  const showFilledNamespacePrompt = !namespace;

  return (
    <div className="min-h-screen bg-gray-50">
      <Header title="DEX Runs" />
      <div className="max-w-[95%] 2xl:max-w-[90%] mx-auto p-4">
        <div className="bg-white shadow-sm border border-gray-200 rounded-lg p-4 mb-4">
          <div className="flex flex-wrap items-end gap-4">
            <FilterInput
              label="Flow type"
              testId="flowType-input"
              value={flowType}
              onChange={(v) => updateFilter('flowType', v)}
              placeholder="(any)"
            />
            <FilterSelect
              label="Status"
              testId="status-select"
              value={status}
              onChange={(v) => updateFilter('status', v)}
              options={RUN_STATUSES.map((s) => ({ value: s.value, label: s.label }))}
              extraOption={{ value: '', label: 'Any status' }}
            />
            <FilterSelect
              label="Order by"
              testId="orderBy-select"
              value={orderBy}
              onChange={(v) => updateFilter('orderBy', v)}
              options={[
                { value: 0, label: 'Start time desc' },
                { value: 1, label: 'Updated at desc' },
              ]}
            />
          </div>
        </div>

        {showFilledNamespacePrompt && (
          <div className="bg-yellow-50 border border-yellow-200 text-yellow-800 rounded-lg p-4 text-sm">
            Enter a <strong>namespace</strong> in the top bar to list runs.
          </div>
        )}

        {namespace && (
          <div className="bg-white shadow-sm border border-gray-200 rounded-lg overflow-hidden">
            {loading && <div className="p-4 text-sm text-gray-500">Loading…</div>}
            {error && (
              <div className="p-4 text-sm text-red-700 bg-red-50 border-b border-red-200">{error}</div>
            )}
            <div className="overflow-x-auto">
              <table data-testid="runs-table" className="w-full whitespace-nowrap text-sm">
                <thead className="bg-gray-100 text-gray-700">
                  <tr>
                    <Th>Run ID</Th>
                    <Th>Status</Th>
                    <Th>Flow type</Th>
                    <Th>Started</Th>
                    <Th>Updated</Th>
                  </tr>
                </thead>
                <tbody>
                  {runs && runs.length === 0 && !loading && (
                    <tr>
                      <td colSpan={5} className="text-center py-8 text-gray-500">
                        No runs found.
                      </td>
                    </tr>
                  )}
                  {runs?.map((r) => (
                    <RunRow key={r.runId} run={r} />
                  ))}
                </tbody>
              </table>
            </div>

            {runs && runs.length > 0 && (
              <div className="flex justify-between items-center px-4 py-3 border-t border-gray-200">
                <span className="text-xs text-gray-500">
                  Page {pageStack.length} · {runs.length} runs
                </span>
                <div className="flex gap-2">
                  <button
                    data-testid="prev-page"
                    type="button"
                    onClick={onPrev}
                    disabled={pageStack.length <= 1 || loading}
                    className={`px-3 py-1 rounded text-sm ${
                      pageStack.length <= 1
                        ? 'bg-gray-100 text-gray-400 cursor-not-allowed'
                        : 'bg-blue-50 text-blue-700 hover:bg-blue-100'
                    }`}
                  >
                    Previous
                  </button>
                  <button
                    data-testid="next-page"
                    type="button"
                    onClick={onNext}
                    disabled={!nextPageToken || loading}
                    className={`px-3 py-1 rounded text-sm ${
                      !nextPageToken
                        ? 'bg-gray-100 text-gray-400 cursor-not-allowed'
                        : 'bg-blue-50 text-blue-700 hover:bg-blue-100'
                    }`}
                  >
                    Next
                  </button>
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function Th({ children }: { children: React.ReactNode }) {
  return (
    <th className="text-left font-medium text-xs uppercase tracking-wide px-3 py-2 border-b border-gray-200">
      {children}
    </th>
  );
}

function RunRow({ run }: { run: RunSummary }) {
  const search = useSearchParams();
  const ns = search.get('namespace') ?? run.namespace;
  const href = useMemo(
    () => `/flow/show?namespace=${encodeURIComponent(ns)}&runId=${encodeURIComponent(run.runId)}`,
    [ns, run.runId],
  );
  return (
    <tr data-testid="run-row" data-run-id={run.runId} className="hover:bg-gray-50">
      <td className="px-3 py-2 border-b border-gray-100 font-mono text-xs">
        <Link
          data-testid="run-link"
          href={href}
          className="text-blue-600 hover:text-blue-800 hover:underline"
        >
          {run.runId}
        </Link>
      </td>
      <td className="px-3 py-2 border-b border-gray-100">
        <StatusBadge status={run.status} />
      </td>
      <td className="px-3 py-2 border-b border-gray-100">{run.flowType || '—'}</td>
      <td className="px-3 py-2 border-b border-gray-100 text-gray-700">
        {formatTimestamp(run.startTimeMs)}
      </td>
      <td className="px-3 py-2 border-b border-gray-100 text-gray-700">
        {formatTimestamp(run.updatedAtMs)}
      </td>
    </tr>
  );
}

function FilterInput({
  label,
  testId,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  testId: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  const [local, setLocal] = useState(value);
  useEffect(() => setLocal(value), [value]);
  return (
    <div className="flex flex-col">
      <label className="text-xs font-medium text-gray-600 mb-1">{label}</label>
      <input
        data-testid={testId}
        type="text"
        value={local}
        onChange={(e) => setLocal(e.target.value)}
        onBlur={() => onChange(local)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') onChange(local);
        }}
        placeholder={placeholder}
        className="border border-gray-300 rounded px-2 py-1 text-sm w-48"
      />
    </div>
  );
}

function FilterSelect({
  label,
  testId,
  value,
  onChange,
  options,
  extraOption,
}: {
  label: string;
  testId: string;
  value: number | null;
  onChange: (v: string) => void;
  options: { value: number; label: string }[];
  extraOption?: { value: string; label: string };
}) {
  const selected = value === null && extraOption ? extraOption.value : String(value);
  return (
    <div className="flex flex-col">
      <label className="text-xs font-medium text-gray-600 mb-1">{label}</label>
      <select
        data-testid={testId}
        value={selected}
        onChange={(e) => onChange(e.target.value)}
        className="border border-gray-300 rounded px-2 py-1 text-sm bg-white"
      >
        {extraOption && <option value={extraOption.value}>{extraOption.label}</option>}
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </div>
  );
}

export default function ListPage() {
  return (
    <Suspense fallback={<div className="p-4 text-sm text-gray-500">Loading…</div>}>
      <ListInner />
    </Suspense>
  );
}
