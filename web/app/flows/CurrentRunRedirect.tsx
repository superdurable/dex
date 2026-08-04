// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import type { FlowSummary } from '@/lib/types';

export function CurrentRunRedirect({ flowId }: { flowId: string }) {
  const navigate = useNavigate();
  const [error, setError] = useState('');

  useEffect(() => {
    const controller = new AbortController();
    void fetch(`/api/flows/summary?flowId=${encodeURIComponent(flowId)}`, {
      signal: controller.signal,
    })
      .then(async (response) => {
        const data = await response.json() as FlowSummary & { error?: string };
        if (!response.ok) throw new Error(data.error || 'Flow lookup failed');
        navigate(`/flows/${encodeURIComponent(flowId)}/${encodeURIComponent(data.runId)}`, {
          replace: true,
        });
      })
      .catch((lookupError) => {
        if (lookupError instanceof DOMException && lookupError.name === 'AbortError') return;
        setError(lookupError instanceof Error ? lookupError.message : 'Flow lookup failed');
      });
    return () => controller.abort();
  }, [flowId, navigate]);

  if (error) return <div className="page-shell"><div className="error-banner">{error}</div></div>;
  return <div className="page-loading">Resolving current run…</div>;
}
