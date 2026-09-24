// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useRef, useState, type FormEvent } from 'react';
import type { FlowV2StartDefinition, FlowV2StartInputSchema } from '@superdurable/flow-definition-renderer';
import { DexAPIError, readResponseJSON } from '@/lib/http';
import { definitionRevisionHeaders, dexFetch } from '@/lib/webConfig';
import {
  createMapEntry,
  createStartInputDraft,
  serializeStartInput,
  type StartInputDraft,
  type StartInputError,
} from './startInput';

interface StartFlowResult {
  runId: string;
}

export function StartFlowDialog({
  definition,
  definitionRevision,
  flowType,
  onClose,
  onDefinitionChanged,
  onStarted,
}: {
  definition: FlowV2StartDefinition;
  definitionRevision: string;
  flowType: string;
  onClose: () => void;
  onDefinitionChanged: (error: unknown) => boolean;
  onStarted: (flowID: string, runID: string) => void;
}) {
  const [flowID, setFlowID] = useState('');
  const [workerAddress, setWorkerAddress] = useState('');
  const [draft, setDraft] = useState(() => createStartInputDraft(definition.input));
  const [errors, setErrors] = useState<StartInputError[]>([]);
  const [submitError, setSubmitError] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const dialogRef = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    if (dialogRef.current && !dialogRef.current.open) dialogRef.current.showModal();
    dialogRef.current?.querySelector<HTMLInputElement>('#start-flow-id')?.focus();
  }, []);

  const updateDraft = () => setDraft({ ...draft });
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const nextErrors: StartInputError[] = [];
    if (flowID.trim() === '') nextErrors.push({ path: 'flowId', message: 'Flow ID is required.' });
    if (workerAddress.trim() === '') nextErrors.push({ path: 'workerTargetAddress', message: 'Worker address is required.' });
    const input = serializeStartInput(definition.input, draft);
    nextErrors.push(...input.errors);
    setErrors(nextErrors);
    setSubmitError('');
    if (nextErrors.length > 0) {
      focusPath(dialogRef.current, nextErrors[0].path);
      return;
    }
    const requestPrefix = JSON.stringify({
      flowType,
      flowId: flowID.trim(),
      workerTargetAddress: workerAddress.trim(),
    });
    const body = `${requestPrefix.slice(0, -1)},"input":${input.json}}`;
    setSubmitting(true);
    try {
      const result = await dexFetch('/api/v2/start', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...definitionRevisionHeaders(definitionRevision) },
        body,
      }).then((response) => readResponseJSON<StartFlowResult>(response));
      onStarted(flowID.trim(), result.runId);
    } catch (error) {
      if (onDefinitionChanged(error)) {
        onClose();
        return;
      }
      setSubmitError(error instanceof DexAPIError || error instanceof Error ? error.message : 'Start Flow failed');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <dialog
      aria-labelledby="start-flow-title"
      className="sfd"
      ref={dialogRef}
      onCancel={(event) => {
        event.preventDefault();
        if (!submitting) onClose();
      }}
    >
      <form onSubmit={(event) => void submit(event)}>
        <header className="sfd-head">
          <div>
            <h2 id="start-flow-title">Start {flowType}</h2>
            <p>{definition.stepType}</p>
          </div>
          <button aria-label="Close Start Flow" disabled={submitting} onClick={onClose} type="button">×</button>
        </header>
        <div className="sfd-scroll">
          <label className="sfd-field">
            <span>Flow ID</span>
            <input
              data-start-path="flowId"
              id="start-flow-id"
              value={flowID}
              onChange={(event) => setFlowID(event.target.value)}
            />
            <FieldError errors={errors} path="flowId" />
          </label>
          <label className="sfd-field">
            <span>Worker gRPC address</span>
            <input
              data-start-path="workerTargetAddress"
              placeholder="worker:9000"
              value={workerAddress}
              onChange={(event) => setWorkerAddress(event.target.value)}
            />
            <FieldError errors={errors} path="workerTargetAddress" />
          </label>
          <fieldset className="sfd-input">
            <legend>Start input</legend>
            <SchemaInput
              draft={draft}
              errors={errors}
              label="Input"
              path="$"
              required
              schema={definition.input}
              onChange={updateDraft}
            />
          </fieldset>
          {submitError && <div className="error-banner" role="alert">{submitError}</div>}
        </div>
        <footer className="sfd-actions">
          <button disabled={submitting} onClick={onClose} type="button">Cancel</button>
          <button className="button primary" disabled={submitting} type="submit">
            {submitting ? 'Starting…' : 'Start Flow'}
          </button>
        </footer>
      </form>
    </dialog>
  );
}

function SchemaInput({ draft, errors, label, path, required, schema, onChange }: {
  draft: StartInputDraft;
  errors: StartInputError[];
  label: string;
  path: string;
  required: boolean;
  schema: FlowV2StartInputSchema;
  onChange: () => void;
}) {
  const isIncluded = draft.mode !== 'omitted';
  const valueControls = (
    <>
      {!required && (
        <label className="sfd-toggle">
          <input
            checked={isIncluded}
            type="checkbox"
            onChange={(event) => { draft.mode = event.target.checked ? 'value' : 'omitted'; onChange(); }}
          />
          Include
        </label>
      )}
      {isIncluded && schema.nullable && (
        <select
          aria-label={`${label} value mode`}
          value={draft.mode === 'null' ? 'null' : 'value'}
          onChange={(event) => { draft.mode = event.target.value === 'null' ? 'null' : 'value'; onChange(); }}
        >
          <option value="value">Value</option>
          <option value="null">Null</option>
        </select>
      )}
    </>
  );
  if (!isIncluded || draft.mode === 'null' || schema.kind === 'null') {
    return (
      <div className="sfd-row">
        <span className="sfd-label">{label}</span>
        {valueControls}
        {schema.kind === 'null' && <code>null</code>}
      </div>
    );
  }
  if (schema.kind === 'object') {
    return (
      <fieldset className="sfd-group" data-start-path={path} tabIndex={-1}>
        <legend>{label}</legend>
        {valueControls}
        {(schema.fields ?? []).map((field) => (
          <SchemaInput
            draft={draft.fields[field.name]}
            errors={errors}
            key={field.name}
            label={field.name}
            path={`${path}.${field.name}`}
            required={field.required}
            schema={field.schema}
            onChange={onChange}
          />
        ))}
      </fieldset>
    );
  }
  if (schema.kind === 'array') {
    const itemSchema = schema.items;
    if (!itemSchema) return null;
    return (
      <fieldset className="sfd-group" data-start-path={path} tabIndex={-1}>
        <legend>{label}</legend>
        {valueControls}
        {draft.items.map((item, index) => (
          <div className="sfd-collection" key={index}>
            <SchemaInput
              draft={item}
              errors={errors}
              label={`Item ${index + 1}`}
              path={`${path}[${index}]`}
              required
              schema={itemSchema}
              onChange={onChange}
            />
            {schema.fixedLength === undefined && (
              <button type="button" onClick={() => { draft.items.splice(index, 1); onChange(); }}>Remove</button>
            )}
          </div>
        ))}
        {schema.fixedLength === undefined && (
          <button type="button" onClick={() => { draft.items.push(createStartInputDraft(itemSchema)); onChange(); }}>
            Add item
          </button>
        )}
        <FieldError errors={errors} path={path} />
      </fieldset>
    );
  }
  if (schema.kind === 'map') {
    const valueSchema = schema.values;
    if (!valueSchema) return null;
    return (
      <fieldset className="sfd-group" data-start-path={path} tabIndex={-1}>
        <legend>{label}</legend>
        {valueControls}
        {draft.entries.map((entry, index) => (
          <div className="sfd-map" key={entry.id}>
            <input
              aria-label={`${label} key ${index + 1}`}
              placeholder="Key"
              value={entry.key}
              onChange={(event) => { entry.key = event.target.value; onChange(); }}
            />
            <SchemaInput
              draft={entry.value}
              errors={errors}
              label="Value"
              path={`${path}[${JSON.stringify(entry.key)}]`}
              required
              schema={valueSchema}
              onChange={onChange}
            />
            <button type="button" onClick={() => { draft.entries.splice(index, 1); onChange(); }}>Remove</button>
          </div>
        ))}
        <button type="button" onClick={() => { draft.entries.push(createMapEntry(schema)); onChange(); }}>Add entry</button>
        <FieldError errors={errors} path={path} />
      </fieldset>
    );
  }
  return (
    <label className="sfd-field">
      <span>{label}{required ? ' *' : ''}</span>
      {valueControls}
      <ScalarInput draft={draft} label={label} path={path} schema={schema} onChange={onChange} />
      <FieldError errors={errors} path={path} />
    </label>
  );
}

function ScalarInput({ draft, label, path, schema, onChange }: {
  draft: StartInputDraft;
  label: string;
  path: string;
  schema: FlowV2StartInputSchema;
  onChange: () => void;
}) {
  if (schema.enumValues) {
    return (
      <select data-start-path={path} value={draft.scalar} onChange={(event) => { draft.scalar = event.target.value; onChange(); }}>
        {schema.enumValues.map((option) => (
          <option key={option.name} value={option.value.toString()}>{option.name} ({option.value})</option>
        ))}
      </select>
    );
  }
  if (schema.kind === 'boolean') {
    return (
      <select
        aria-label={label}
        data-start-path={path}
        value={draft.booleanValue ? 'true' : 'false'}
        onChange={(event) => { draft.booleanValue = event.target.value === 'true'; onChange(); }}
      >
        <option value="false">false</option>
        <option value="true">true</option>
      </select>
    );
  }
  if (schema.format === 'date-time') {
    return (
      <input
        data-start-path={path}
        type="datetime-local"
        value={localDateTime(draft.scalar)}
        onChange={(event) => { draft.scalar = event.target.value ? new Date(event.target.value).toISOString() : ''; onChange(); }}
      />
    );
  }
  return (
    <input
      data-start-path={path}
      inputMode={schema.kind === 'integer' || schema.kind === 'number' ? 'decimal' : undefined}
      type="text"
      value={draft.scalar}
      onChange={(event) => { draft.scalar = event.target.value; onChange(); }}
    />
  );
}

function FieldError({ errors, path }: { errors: StartInputError[]; path: string }) {
  const error = errors.find((candidate) => candidate.path === path);
  return error ? <small className="sfd-error" role="alert">{error.message}</small> : null;
}

function focusPath(dialog: HTMLDialogElement | null, path: string) {
  const element = Array.from(dialog?.querySelectorAll<HTMLElement>('[data-start-path]') ?? [])
    .find((candidate) => candidate.dataset.startPath === path);
  element?.focus();
}

function localDateTime(value: string): string {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return '';
  const offset = date.getTimezoneOffset() * 60_000;
  return new Date(date.valueOf() - offset).toISOString().slice(0, 16);
}
