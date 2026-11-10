// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { Waves } from 'lucide-react';
import { hydrateBlobs } from '@/lib/blobs';
import { formatDate } from '@/lib/format';
import { readResponseJSON } from '@/lib/http';
import type { FlowDefinitionEntry, StreamMessage } from '@/lib/types';
import { StructuredValue } from '../../components/StructuredValue';
import { usePreferences } from '../../providers';

interface StreamPanelProps {
  flowId: string;
  flowType: string;
  definition?: FlowDefinitionEntry | null;
}

/** A Stream declared in a Flow Definition Graph. */
export interface DefinedStream {
  name: string;
  valueType: string;
}

const OTHER_STREAM_CHOICE = '\u0000other';

/** The Streams a loaded definition declares, in graph order and without duplicate names. */
export function definedStreamsOf(definition: FlowDefinitionEntry | null | undefined): DefinedStream[] {
  const streams: DefinedStream[] = [];
  const seenNames = new Set<string>();
  for (const node of definition?.graph.nodes ?? []) {
    if (node.kind !== 'stream' || !node.name || seenNames.has(node.name)) continue;
    seenNames.add(node.name);
    streams.push({ name: node.name, valueType: node.resource?.valueType ?? '' });
  }
  return streams;
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

export function StreamPanel({ flowId, flowType, definition }: StreamPanelProps) {
  const { timezone } = usePreferences();
  const definedStreams = definedStreamsOf(definition);
  const hasDefinedStreams = definedStreams.length > 0;
  const [pickedStreamChoice, setPickedStreamChoice] = useState<string | null>(null);
  const streamChoice = pickedStreamChoice ?? (definedStreams.length === 1 ? definedStreams[0].name : '');
  const isTypingStreamName = !hasDefinedStreams || streamChoice === OTHER_STREAM_CHOICE;
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
            const hydrated = await hydrateBlobs(
              flowId,
              rawMessage.value,
              blobCache.current,
              controller.signal,
            );
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
    const nextStreamName = isTypingStreamName ? streamName.trim() : streamChoice;
    if (!nextStreamName) {
      setError(isTypingStreamName ? 'Enter a Stream name.' : 'Choose a Stream.');
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
        <h2 className="sr-only">Stream messages</h2>
        {activeStreamName && (
          <span className="stream-listening-status" aria-live="polite">
            <span className={isListening ? 'stream-listening-dot' : ''} />
            {isListening ? 'Listening' : 'Stopped'}
          </span>
        )}
      </div>

      <form className="stream-form" onSubmit={startListening}>
        <label htmlFor={hasDefinedStreams ? 'stream-choice' : 'stream-name'}>Stream name</label>
        <div>
          {hasDefinedStreams && (
            <span className="stream-choice">
              <Waves aria-hidden="true" size={15} />
              <select
                id="stream-choice"
                name="streamChoice"
                onChange={(event) => {
                  setPickedStreamChoice(event.target.value);
                  setError('');
                }}
                value={streamChoice}
              >
                {streamChoice === '' && <option disabled value="">Choose a Stream…</option>}
                {definedStreams.map((stream) => (
                  <option key={stream.name} value={stream.name}>
                    {stream.valueType ? `${stream.name} · ${stream.valueType}` : stream.name}
                  </option>
                ))}
                <option value={OTHER_STREAM_CHOICE}>Other name…</option>
              </select>
            </span>
          )}
          {isTypingStreamName && (
            <input
              aria-label={hasDefinedStreams ? 'Other Stream name' : undefined}
              autoFocus={hasDefinedStreams}
              id="stream-name"
              name="streamName"
              onChange={(event) => setStreamName(event.target.value)}
              placeholder="a defined stream name"
              value={streamName}
            />
          )}
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
                {message.source && <code>{message.source}</code>}
              </div>
              <StructuredValue value={message.value} />
            </article>
          ))}
        </div>
      )}
    </section>
  );
}
