'use client';

import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import type { FlowSummary } from '@/lib/types';

export function CurrentRunRedirect({ flowId }: { flowId: string }) {
  const router = useRouter();
  const [error, setError] = useState('');

  useEffect(() => {
    const controller = new AbortController();
    void fetch(`/api/flows/summary?flowId=${encodeURIComponent(flowId)}`, {
      signal: controller.signal,
    })
      .then(async (response) => {
        const data = await response.json() as FlowSummary & { error?: string };
        if (!response.ok) throw new Error(data.error || 'Flow lookup failed');
        router.replace(`/flows/${encodeURIComponent(flowId)}/${encodeURIComponent(data.runId)}`);
      })
      .catch((lookupError) => {
        if (lookupError instanceof DOMException && lookupError.name === 'AbortError') return;
        setError(lookupError instanceof Error ? lookupError.message : 'Flow lookup failed');
      });
    return () => controller.abort();
  }, [flowId, router]);

  if (error) return <div className="page-shell"><div className="error-banner">{error}</div></div>;
  return <div className="page-loading">Resolving current run…</div>;
}
