// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { Eraser, Square, Waves } from 'lucide-react';
import { hydrateBlobs } from '@/lib/blobs';
import { readResponseJSON } from '@/lib/http';
import type { FlowDefinitionEntry, StreamMessage } from '@/lib/types';
import { usePreferences } from '../../providers';
import {
  appendStreamEntry,
  EMPTY_STREAM_ENTRIES,
  KEPT_STREAM_MESSAGE_LIMIT_CHOICES,
  readStoredKeptStreamMessageLimit,
  storeKeptStreamMessageLimit,
  StreamCountAnnouncement,
  streamEntryContentOf,
  StreamOutput,
  StreamStatusBadge,
  trimStreamEntries,
} from './StreamOutput';
import type { StreamEntriesState, StreamListeningStatus } from './StreamOutput';
import { webURL } from '@/lib/basePath';

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
    const response = await fetcher(webURL(`/api/flows/stream?${params}`), {
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
  const isPickedChoiceAvailable = pickedStreamChoice === OTHER_STREAM_CHOICE
    || definedStreams.some((stream) => stream.name === pickedStreamChoice);
  const defaultStreamChoice = definedStreams.length === 1 ? definedStreams[0].name : '';
  const streamChoice = isPickedChoiceAvailable && pickedStreamChoice !== null ? pickedStreamChoice : defaultStreamChoice;
  const isTypingStreamName = !hasDefinedStreams || streamChoice === OTHER_STREAM_CHOICE;
  const [streamName, setStreamName] = useState('');
  const [activeStream, setActiveStream] = useState<DefinedStream | null>(null);
  const activeStreamName = activeStream?.name ?? '';
  const [entries, setEntries] = useState<StreamEntriesState>(EMPTY_STREAM_ENTRIES);
  const lastSequenceNumberRef = useRef(0);
  const [keptMessageLimit, setKeptMessageLimit] = useState(readStoredKeptStreamMessageLimit);
  // The listener reads the limit through a ref so changing it does not restart listening.
  const keptMessageLimitRef = useRef(keptMessageLimit);
  const [isListening, setIsListening] = useState(false);
  const [isStopped, setIsStopped] = useState(false);
  const [formError, setFormError] = useState('');
  const [readError, setReadError] = useState('');
  const [blobWarning, setBlobWarning] = useState('');
  const [session, setSession] = useState(0);
  const blobCache = useRef(new Map<string, unknown>());

  useEffect(() => {
    if (!activeStream || isStopped) return;
    const controller = new AbortController();
    let isActive = true;

    const read = async () => {
      setIsListening(true);
      try {
        await listenToStream({
          flowId,
          flowType,
          streamName: activeStream.name,
          signal: controller.signal,
          onMessage: async (rawMessage) => {
            const hydrated = await hydrateBlobs(
              flowId,
              rawMessage.value,
              blobCache.current,
              controller.signal,
            );
            if (!isActive) return;
            // Classify once on arrival, outside the updater, so a re-render never repeats it.
            lastSequenceNumberRef.current += 1;
            const record = {
              message: { ...rawMessage, value: hydrated.value },
              content: streamEntryContentOf(hydrated.value, activeStream.valueType),
              sequenceNumber: lastSequenceNumberRef.current,
            };
            setEntries((current) => appendStreamEntry(current, record, keptMessageLimitRef.current));
            if (hydrated.error) setBlobWarning(hydrated.error);
          },
        });
      } catch (readError) {
        if (controller.signal.aborted) return;
        setReadError(readError instanceof Error ? readError.message : 'Stream read failed');
        setIsListening(false);
      }
    };
    void read();
    return () => {
      isActive = false;
      controller.abort();
    };
  }, [activeStream, flowId, flowType, isStopped, session]);

  const startListening = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const nextStreamName = isTypingStreamName ? streamName.trim() : streamChoice;
    if (!nextStreamName) {
      setFormError(isTypingStreamName ? 'Enter a Stream name.' : 'Choose a Stream.');
      return;
    }
    blobCache.current.clear();
    lastSequenceNumberRef.current = 0;
    setEntries(EMPTY_STREAM_ENTRIES);
    setFormError('');
    setReadError('');
    setIsStopped(false);
    setBlobWarning('');
    setActiveStream({
      name: nextStreamName,
      valueType: definedStreams.find((stream) => stream.name === nextStreamName)?.valueType ?? '',
    });
    setSession((current) => current + 1);
  };

  const clearMessages = () => {
    lastSequenceNumberRef.current = 0;
    setEntries(EMPTY_STREAM_ENTRIES);
    setBlobWarning('');
  };

  const changeKeptMessageLimit = (limit: number) => {
    keptMessageLimitRef.current = limit;
    setKeptMessageLimit(limit);
    storeKeptStreamMessageLimit(limit);
    setEntries((current) => trimStreamEntries(current, limit));
  };

  const stopListening = () => {
    setIsStopped(true);
    setIsListening(false);
  };

  const listeningStatus: StreamListeningStatus = readError
    ? 'error'
    : isListening && !isStopped ? 'listening' : 'stopped';
  const messageCount = entries.droppedMessageCount + entries.records.length;

  return (
    <section className="stream-panel">
      <h2 className="sr-only">Stream messages</h2>
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
                  setFormError('');
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
              onChange={(event) => {
                setStreamName(event.target.value);
                // A name typed before the definitions load keeps the text box when they arrive.
                if (!hasDefinedStreams) setPickedStreamChoice(OTHER_STREAM_CHOICE);
              }}
              placeholder="a defined stream name"
              value={streamName}
            />
          )}
          <button className="button primary" type="submit">
            {activeStreamName ? 'Restart listening' : 'Start listening'}
          </button>
        </div>
      </form>

      {formError && <div className="error-banner">{formError}</div>}

      {activeStreamName ? (
        <div className="stream-session">
          <div className="stream-session-header">
            <StreamStatusBadge status={listeningStatus} />
            <code className="stream-session-name">{activeStreamName}</code>
            {messageCount > 0 && (
              <span className="stream-session-count">
                {messageCount} {messageCount === 1 ? 'message' : 'messages'}
              </span>
            )}
            <StreamCountAnnouncement messageCount={messageCount} />
            <span className="stream-session-actions">
              <label className="stream-keep-limit">
                Keep last
                <select
                  name="keptMessageLimit"
                  onChange={(event) => changeKeptMessageLimit(Number(event.target.value))}
                  value={keptMessageLimit}
                >
                  {KEPT_STREAM_MESSAGE_LIMIT_CHOICES.map((choice) => (
                    <option key={choice} value={choice}>
                      {choice.toLocaleString('en-US')}
                    </option>
                  ))}
                </select>
              </label>
              {listeningStatus === 'listening' && (
                <button className="button ghost" onClick={stopListening} type="button">
                  <Square aria-hidden="true" size={13} />
                  Stop
                </button>
              )}
              {messageCount > 0 && (
                <button className="button ghost" onClick={clearMessages} type="button">
                  <Eraser aria-hidden="true" size={13} />
                  Clear
                </button>
              )}
            </span>
          </div>
          {readError && <div className="error-banner">{readError}</div>}
          {blobWarning && <div className="warning-banner">{blobWarning}</div>}
          {entries.records.length > 0 ? (
            <StreamOutput entries={entries} status={listeningStatus} timezone={timezone} />
          ) : (
            !readError && (
              <div className="stream-empty">
                <strong>{listeningStatus === 'listening' ? 'Waiting for the first message' : 'No messages'}</strong>
                <span>
                  {listeningStatus === 'listening'
                    ? 'Each message the Flow writes to this Stream appears here as it arrives. Text reads as a live transcript, and progress updates show a bar.'
                    : 'Start listening again to read this Stream.'}
                </span>
              </div>
            )
          )}
        </div>
      ) : (
        <div className="stream-empty">
          <Waves aria-hidden="true" size={20} />
          <strong>Choose a Stream to watch</strong>
          <span>Messages the Flow writes to it appear here live. Entries show the newest at the top.</span>
        </div>
      )}
    </section>
  );
}
