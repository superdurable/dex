// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useId, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { X } from 'lucide-react';
import { parseCodecEndpointURL, payloadCodecErrorMessage, type CodecEndpointMode } from '@/lib/payloadCodec';
import { usePayloadCodec } from '../../payloadCodecContext';

const MODE_LABEL: Record<CodecEndpointMode, string> = { auto: 'Auto', custom: 'Custom', off: 'Off' };

const REMEMBER_HELP = 'Keeps the bearer token in this browser’s local storage, shared by all tabs, until you delete it. '
  + 'Scripts on this Dex Web origin can read it. Turn it off on a shared computer; the token is then kept only for '
  + 'this tab.';
const COOKIES_HELP = 'Sends your browser’s cookies with each request to the codec endpoint. Turn it on only when an '
  + 'SSO proxy in front of the codec server signs you in by cookie. The codec server must allow credentials in its '
  + 'CORS response. Leave it off for dexcli codec-server and for a bearer token.';

type HelpTopic = 'remember' | 'cookies';

interface CodecOptionProps {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: string;
  helpLabel: string;
  help: string;
  isHelpOpen: boolean;
  onToggleHelp: () => void;
}

/** One checkbox row with a "?" button that shows its explanation under the row. */
function CodecOption({ checked, onChange, label, helpLabel, help, isHelpOpen, onToggleHelp }: CodecOptionProps): ReactNode {
  const helpId = useId();
  return (
    <>
      <div className="app-bar-codec-option">
        <label className="app-bar-codec-credentials">
          <input checked={checked} onChange={(event) => onChange(event.target.checked)} type="checkbox" />
          {label}
        </label>
        <button
          aria-controls={helpId}
          aria-expanded={isHelpOpen}
          aria-label={helpLabel}
          className="app-bar-icon-button app-bar-codec-small-button"
          onClick={onToggleHelp}
          type="button"
        >
          ?
        </button>
      </div>
      {isHelpOpen && <small className="app-bar-codec-help" id={helpId}>{help}</small>}
    </>
  );
}

/** Chooses the codec endpoint that decrypts and encrypts payloads in this browser; saved in this browser only. */
export function CodecEndpointSetting() {
  const {
    preference, setPreference, savedBearerTokenEndpoint, saveCustomCodecEndpoint, deleteCustomBearerToken,
    localServer, codec, store, retryDecoding,
  } = usePayloadCodec();
  const [draftEndpoint, setDraftEndpoint] = useState(preference.endpoint);
  const [draftCredentials, setDraftCredentials] = useState(preference.includeCredentials);
  const [draftRemember, setDraftRemember] = useState(preference.rememberBearerToken);
  // Uncontrolled, so a typed token never becomes a value attribute in the DOM.
  const tokenInputRef = useRef<HTMLInputElement>(null);
  const [openHelp, setOpenHelp] = useState<HelpTopic | null>(null);
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
  const clearTokenInput = () => {
    if (tokenInputRef.current) tokenInputRef.current.value = '';
  };
  const saveCustom = () => {
    const parsed = parseCodecEndpointURL(draftEndpoint);
    if ('error' in parsed) {
      setError(parsed.error);
      return;
    }
    setError('');
    setDraftEndpoint(parsed.endpoint);
    const typedToken = tokenInputRef.current?.value.trim() ?? '';
    clearTokenInput();
    saveCustomCodecEndpoint({
      mode: 'custom', endpoint: parsed.endpoint, includeCredentials: draftCredentials, rememberBearerToken: draftRemember,
    }, typedToken);
  };
  const deleteToken = () => {
    clearTokenInput();
    deleteCustomBearerToken();
  };
  const toggleHelp = (topic: HelpTopic) => setOpenHelp((open) => (open === topic ? null : topic));

  const savedEndpoint = parseCodecEndpointURL(preference.endpoint);
  const isTokenForThisURL = savedBearerTokenEndpoint !== null && 'endpoint' in savedEndpoint
    && savedBearerTokenEndpoint === savedEndpoint.endpoint;
  let tokenState = '';
  if (isTokenForThisURL) tokenState = preference.rememberBearerToken ? 'Token saved in this browser.' : 'Token saved for this tab.';
  else if (savedBearerTokenEndpoint !== null) tokenState = 'The saved token is for another URL.';

  let status: string;
  if (codec?.source === 'local') status = `Local dev codec server · ${codec.endpoint}`;
  else if (codec?.source === 'custom') status = codec.endpoint;
  else if (preference.mode === 'off') status = 'Off: encrypted values stay encrypted';
  else if (preference.mode === 'custom') status = 'Enter a codec endpoint URL';
  else status = 'None: encrypted values stay encrypted';

  return (
    <div
      aria-label="Codec endpoint"
      className="app-bar-setting app-bar-codec-setting"
      onKeyDown={(event) => {
        // Escape closes an open explanation first and keeps the settings menu open.
        if (event.key === 'Escape' && openHelp) {
          event.stopPropagation();
          setOpenHelp(null);
        }
      }}
      role="group"
    >
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
            className="app-bar-jump-input"
            onChange={(event) => setDraftEndpoint(event.target.value)}
            placeholder="https://codec.example.com"
            spellCheck={false}
            type="url"
            value={draftEndpoint}
          />
          <small>Enter the bearer token, if the codec endpoint requires one</small>
          <div className="app-bar-codec-token-row">
            <input
              aria-label="Codec endpoint bearer token"
              autoComplete="off"
              className="app-bar-jump-input"
              placeholder={isTokenForThisURL ? 'Replace the saved token' : 'Bearer token'}
              ref={tokenInputRef}
              spellCheck={false}
              type="password"
            />
            {savedBearerTokenEndpoint !== null && (
              <button
                aria-label="Delete saved bearer token"
                className="app-bar-icon-button app-bar-codec-small-button"
                onClick={deleteToken}
                title="Delete saved bearer token from this browser"
                type="button"
              >
                <X aria-hidden="true" size={14} />
              </button>
            )}
          </div>
          {tokenState && <small>{tokenState}</small>}
          <CodecOption
            checked={draftRemember}
            help={REMEMBER_HELP}
            helpLabel="What is Remember on this browser?"
            isHelpOpen={openHelp === 'remember'}
            label="Remember on this browser"
            onChange={setDraftRemember}
            onToggleHelp={() => toggleHelp('remember')}
          />
          <CodecOption
            checked={draftCredentials}
            help={COOKIES_HELP}
            helpLabel="What is Send cookies?"
            isHelpOpen={openHelp === 'cookies'}
            label="Send cookies (SSO proxy)"
            onChange={setDraftCredentials}
            onToggleHelp={() => toggleHelp('cookies')}
          />
          <button className="button primary app-bar-codec-save" type="submit">Save</button>
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
