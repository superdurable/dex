// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useState, useSyncExternalStore } from 'react';
import { parseCodecEndpointURL, payloadCodecErrorMessage, type CodecEndpointMode } from '@/lib/payloadCodec';
import { usePayloadCodec } from '../../payloadCodecContext';

const MODE_LABEL: Record<CodecEndpointMode, string> = { auto: 'Auto', custom: 'Custom', off: 'Off' };

/** Chooses the codec endpoint that decrypts and encrypts payloads in this browser; saved in this browser only. */
export function CodecEndpointSetting() {
  const {
    preference, setPreference, customBearerToken, setCustomBearerToken, localServer, codec, store, retryDecoding,
  } = usePayloadCodec();
  const [draftEndpoint, setDraftEndpoint] = useState(preference.endpoint);
  const [draftCredentials, setDraftCredentials] = useState(preference.includeCredentials);
  // The saved token is never put back into the input, so it stays out of the DOM.
  const [draftToken, setDraftToken] = useState('');
  const [error, setError] = useState('');
  useSyncExternalStore(store.subscribe, store.getVersion, store.getVersion);
  const failure = store.failure();

  const chooseMode = (mode: CodecEndpointMode) => {
    setError('');
    if (mode === 'custom') {
      const parsed = parseCodecEndpointURL(draftEndpoint);
      if ('error' in parsed) {
        setPreference({ ...preference, mode: 'custom' });
        return;
      }
    }
    setPreference({ ...preference, mode });
  };
  const saveCustom = () => {
    const parsed = parseCodecEndpointURL(draftEndpoint);
    if ('error' in parsed) {
      setError(parsed.error);
      return;
    }
    setError('');
    setDraftEndpoint(parsed.endpoint);
    setPreference({ mode: 'custom', endpoint: parsed.endpoint, includeCredentials: draftCredentials });
    const token = draftToken.trim();
    if (token) setCustomBearerToken(token);
    setDraftToken('');
  };
  const clearToken = () => {
    setDraftToken('');
    setCustomBearerToken('');
  };

  let status: string;
  if (codec?.source === 'local') status = `Local dev codec server · ${codec.endpoint}`;
  else if (codec?.source === 'custom') status = codec.endpoint;
  else if (preference.mode === 'off') status = 'Off: encrypted values stay encrypted';
  else if (preference.mode === 'custom') status = 'Enter a codec endpoint URL';
  else status = 'None: encrypted values stay encrypted';

  return (
    <div aria-label="Codec endpoint" className="app-bar-setting app-bar-codec-setting" role="group">
      <span>Codec endpoint</span>
      <div className="app-bar-segmented">
        {(['auto', 'custom', 'off'] as const).map((mode) => (
          <button
            aria-pressed={preference.mode === mode}
            key={mode}
            onClick={() => chooseMode(mode)}
            title={mode === 'auto' ? 'Use the local codec server of dexcli dev when there is one' : undefined}
            type="button"
          >
            {MODE_LABEL[mode]}
          </button>
        ))}
      </div>
      <small className="app-bar-codec-status" data-testid="codec-endpoint-status">{status}</small>
      {preference.mode === 'auto' && localServer?.configured === false && (
        <small className="muted">Dex Web reports no local codec server.</small>
      )}
      {preference.mode === 'custom' && (
        <form
          className="app-bar-codec-form"
          onSubmit={(event) => {
            event.preventDefault();
            saveCustom();
          }}
        >
          <input
            aria-label="Codec endpoint URL"
            onChange={(event) => setDraftEndpoint(event.target.value)}
            placeholder="https://codec.example.com"
            spellCheck={false}
            type="url"
            value={draftEndpoint}
          />
          <input
            aria-label="Codec endpoint bearer token"
            autoComplete="off"
            onChange={(event) => setDraftToken(event.target.value)}
            placeholder={customBearerToken ? 'New bearer token to replace it' : 'Bearer token (optional)'}
            spellCheck={false}
            type="password"
            value={draftToken}
          />
          {(customBearerToken || draftToken) && (
            <div className="app-bar-codec-token-state">
              <small className="muted">
                {customBearerToken ? 'Bearer token saved for this tab.' : 'Save to use this bearer token.'}
              </small>
              <button onClick={clearToken} type="button">Clear token</button>
            </div>
          )}
          <label className="app-bar-codec-credentials">
            <input
              checked={draftCredentials}
              onChange={(event) => setDraftCredentials(event.target.checked)}
              type="checkbox"
            />
            Send cookies (SSO proxy)
          </label>
          <button type="submit">Save</button>
          {error && <small className="field-error">{error}</small>}
        </form>
      )}
      {codec && failure && (
        <small className="field-error" role="status">
          {payloadCodecErrorMessage(failure, codec)}{' '}
          <button className="app-bar-codec-retry" onClick={retryDecoding} type="button">Retry</button>
        </small>
      )}
      <small className="muted">Decrypted values stay in this page&apos;s memory. Dex Web never receives keys or plaintext.</small>
    </div>
  );
}
