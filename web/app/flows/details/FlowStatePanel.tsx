import type { FlowHistoryEvent, FlowState, FlowSummary } from '@/lib/types';
import { waitingConditionTypeLabel } from '@/lib/semantic';
import { JsonView } from '../../components/JsonView';
import { EventDetails, eventTitle } from './EventDetails';

type Data = Record<string, unknown>;

function data(value: unknown): Data {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Data : {};
}

function normalizeWaitingCondition(value: unknown): Data {
  const condition = data(value);
  return {
    ...condition,
    waitingConditionType: waitingConditionTypeLabel(condition.waitingConditionType),
  };
}

export function FlowStatePanel({
  state,
  selectedEvent,
  summary,
}: {
  state: FlowState | null;
  selectedEvent: FlowHistoryEvent | null;
  summary: FlowSummary | null;
}) {
  return (
    <div className="sidebar-stack">
      {selectedEvent && (
        <section className="sidebar-section">
          <p className="eyebrow">Selected event</p>
          <h3>{eventTitle(selectedEvent)}</h3>
          <div className="event-meta">
            <span>Event {selectedEvent.eventId}</span>
            <span>{selectedEvent.eventTime || 'No timestamp'}</span>
          </div>
          <EventDetails event={selectedEvent} />
        </section>
      )}

      <section className="sidebar-section">
        <p className="eyebrow">Live state</p>
        <h3>{state ? 'Interpreter snapshot' : `${summary?.flowStatus || 'Unavailable'}`}</h3>
        {!state && <p className="muted">Live state is queried only while this run is active.</p>}
        {state?.activeStepExecutions.map((step) => (
          <div className="active-step-card" key={step.stepExecutionId}>
            <div>
              <b>{step.stepType}</b>
              <span className={`phase phase-${step.phase.toLowerCase()}`}>{step.phase}</span>
            </div>
            <code>{step.stepExecutionId}</code>
            <span>From {step.fromStepExecutionId || '—'}</span>
            {step.waitingCondition && Object.keys(step.waitingCondition).length > 0 && (
              <JsonView value={normalizeWaitingCondition(step.waitingCondition)} label="Waiting condition" />
            )}
            {step.timers.length > 0 && <JsonView value={step.timers} label={`${step.timers.length} timers`} />}
          </div>
        ))}
        {state && state.activeStepExecutions.length === 0 && (
          <p className="muted">No active step executions.</p>
        )}
      </section>

      {state && (
        <>
          <section className="sidebar-section">
            <p className="eyebrow">Attributes</p>
            <h3>{state.attributes.length} values</h3>
            <JsonView value={state.attributes} label="Attributes" />
          </section>
          <section className="sidebar-section">
            <p className="eyebrow">Queues & channels</p>
            <JsonView value={state.queuedSteps} label={`${state.queuedSteps.length} queued steps`} />
            <JsonView value={state.pendingChannelMessages} label="Pending channel messages" />
            <JsonView value={state.completedSteps} label="Completed outputs" />
          </section>
        </>
      )}
    </div>
  );
}
