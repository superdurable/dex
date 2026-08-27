// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { hydrateBlobs } from '@/lib/blobs';
import { formatDate } from '@/lib/format';
import { readResponseJSON } from '@/lib/http';
import type { StreamMessage } from '@/lib/types';
import { StructuredValue } from '../../components/StructuredValue';
import { usePreferences } from '../../providers';

interface StreamPanelProps {
  flowId: string;
  flowType: string;
}

interface StreamListenerOptions {
  flowId: string;
  flowType: string;
  streamName: string;
  signal: AbortSignal;
  onMessage: (message: StreamMessage) => Promise<void>;
  fetcher?: typeof fetch;
}

export async function listenToStream({
  flowId,
  flowType,
  streamName,
  signal,
  onMessage,
  fetcher = fetch,
}: StreamListenerOptions): Promise<void> {
  let resumeToken = '';
  while (!signal.aborted) {
    const params = new URLSearchParams({
      flowId,
      flowType,
      streamName,
      resumeToken,
    });
    const response = await fetcher(`/api/flows/stream?${params}`, {
      cache: 'no-store',
      signal,
    });
    if (response.status === 408) {
      await response.text();
      continue;
    }
    const rawMessage = await readResponseJSON<StreamMessage>(response);
    resumeToken = rawMessage.resumeToken;
    await onMessage(rawMessage);
  }
}

export function StreamPanel({ flowId, flowType }: StreamPanelProps) {
  const { timezone } = usePreferences();
  const [streamName, setStreamName] = useState('');
  const [activeStreamName, setActiveStreamName] = useState('');
  const [messages, setMessages] = useState<StreamMessage[]>([]);
  const [isListening, setIsListening] = useState(false);
  const [error, setError] = useState('');
  const [blobWarning, setBlobWarning] = useState('');
  const [session, setSession] = useState(0);
  const blobCache = useRef(new Map<string, unknown>());

  useEffect(() => {
    if (!activeStreamName) return;
    const controller = new AbortController();
    let isActive = true;

    const read = async () => {
      setIsListening(true);
      try {
        await listenToStream({
          flowId,
          flowType,
          streamName: activeStreamName,
          signal: controller.signal,
          onMessage: async (rawMessage) => {
            const hydrated = await hydrateBlobs(rawMessage.value, blobCache.current, controller.signal);
            if (!isActive) return;
            setMessages((current) => [...current, { ...rawMessage, value: hydrated.value }]);
            if (hydrated.error) setBlobWarning(hydrated.error);
          },
        });
      } catch (readError) {
        if (controller.signal.aborted) return;
        setError(readError instanceof Error ? readError.message : 'Stream read failed');
        setIsListening(false);
      }
    };
    void read();
    return () => {
      isActive = false;
      controller.abort();
    };
  }, [activeStreamName, flowId, flowType, session]);

  const startListening = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const nextStreamName = streamName.trim();
    if (!nextStreamName) {
      setError('Enter a Stream name.');
      return;
    }
    blobCache.current.clear();
    setMessages([]);
    setError('');
    setBlobWarning('');
    setActiveStreamName(nextStreamName);
    setSession((current) => current + 1);
  };

  return (
    <section className="stream-panel">
      <div className="stream-panel-header">
        <div>
          <p className="eyebrow">Resumable stream</p>
          <h2>Stream messages</h2>
          <p>Messages are best effort and may be trimmed as the Stream reaches its configured capacity.</p>
        </div>
        {activeStreamName && (
          <span className="stream-listening-status" aria-live="polite">
            <span className={isListening ? 'stream-listening-dot' : ''} />
            {isListening ? 'Listening' : 'Stopped'}
          </span>
        )}
      </div>

      <form className="stream-form" onSubmit={startListening}>
        <label htmlFor="stream-name">Stream name</label>
        <div>
          <input
            id="stream-name"
            name="streamName"
            onChange={(event) => setStreamName(event.target.value)}
            placeholder="thinking"
            value={streamName}
          />
          <button className="button primary" type="submit">
            {activeStreamName ? 'Restart listening' : 'Start listening'}
          </button>
        </div>
      </form>

      {error && <div className="error-banner">{error}</div>}
      {blobWarning && <div className="warning-banner">{blobWarning}</div>}

      {activeStreamName && messages.length === 0 && !error && (
        <p className="stream-empty">Listening for messages in <code>{activeStreamName}</code>…</p>
      )}
      {messages.length > 0 && (
        <div className="stream-message-list" aria-live="polite">
          {messages.map((message) => (
            <article className="stream-message" key={message.resumeToken}>
              <div className="stream-message-meta">
                <span>{formatDate(message.createdTime, timezone)}</span>
                {message.idempotencyKey && <code>{message.idempotencyKey}</code>}
              </div>
              <StructuredValue value={message.value} />
            </article>
          ))}
        </div>
      )}
    </section>
  );
}
