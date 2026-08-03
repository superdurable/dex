import { Link } from 'react-router-dom';
import type { FlowHistoryEvent } from '@/lib/types';
import { formatDate } from '@/lib/format';
import { durabilityLabel } from '@/lib/semantic';
import { usePreferences } from '../../providers';
import { eventTitle } from './EventDetails';

function eventTone(event: FlowHistoryEvent) {
  if (event.type.endsWith('Failed')) return 'failed';
  if (event.type === 'StepWaitForCompleted') return 'waiting';
  if (event.type.endsWith('Completed')) return 'completed';
  if (event.type === 'FlowClosed') return 'closed';
  return 'neutral';
}

function executionSummary(event: FlowHistoryEvent): string {
  const execution = event.payload.execution;
  if (!execution || typeof execution !== 'object') return '';
  const info = execution as Record<string, unknown>;
  return [info.stepType, info.stepExecutionId].filter(Boolean).join(' · ');
}

function previousRunID(event: FlowHistoryEvent): string {
  if (event.type !== 'FlowStartedOrContinued') return '';
  const continued = event.payload.continuedStart;
  if (!continued || typeof continued !== 'object') return '';
  const value = (continued as Record<string, unknown>).previousRunId;
  return typeof value === 'string' ? value : '';
}

export function Timeline({
  flowId,
  events,
  selectedEvent,
  onSelectEvent,
}: {
  flowId: string;
  events: FlowHistoryEvent[];
  selectedEvent: FlowHistoryEvent | null;
  onSelectEvent: (event: FlowHistoryEvent) => void;
}) {
  const { timezone } = usePreferences();
  if (!events.length) return <div className="card empty-state"><h3>No semantic events loaded</h3></div>;
  const startMs = events[0].eventTime ? Date.parse(events[0].eventTime) : 0;
  return (
    <div className="timeline-wrap">
      <div className="view-toolbar">
        <div>
          <p className="eyebrow">Dex semantic history</p>
          <h2>{events.length} events</h2>
        </div>
      </div>
      <div className="timeline">
        {events.map((event) => {
          const eventMs = event.eventTime ? Date.parse(event.eventTime) : 0;
          const relative = startMs && eventMs ? Math.max(0, Math.round((eventMs - startMs) / 1000)) : null;
          const selected = selectedEvent?.eventId === event.eventId;
          const previousRunId = previousRunID(event);
          return (
            <article
              className={`timeline-row ${selected ? 'selected' : ''}`}
              key={event.eventId}
              onClick={() => onSelectEvent(event)}
            >
              <div className="timeline-time">
                <b>{formatDate(event.eventTime, timezone)}</b>
                {relative !== null && <span>+{relative}s</span>}
              </div>
              <div className="timeline-rail">
                <span className={`timeline-dot tone-${eventTone(event)}`} />
              </div>
              <div className="event-card">
                <header>
                  <div>
                    <span className="event-id">#{event.eventId}</span>
                    <h3>
                      {previousRunId ? (
                        <Link
                          className="event-run-link"
                          title={previousRunId}
                          to={`/flows/${encodeURIComponent(flowId)}/${encodeURIComponent(previousRunId)}`}
                        >
                          Flow continued
                        </Link>
                      ) : eventTitle(event)}
                    </h3>
                    {executionSummary(event) && <p>{executionSummary(event)}</p>}
                  </div>
                  <span className={`event-type tone-${eventTone(event)}`}>{event.type}</span>
                </header>
                <EventHighlights event={event} />
              </div>
            </article>
          );
        })}
      </div>
    </div>
  );
}

function EventHighlights({ event }: { event: FlowHistoryEvent }) {
  const execution = event.payload.execution as Record<string, unknown> | undefined;
  const failure = event.payload.failure as Record<string, unknown> | undefined;
  if (!execution && !failure) return null;
  return (
    <div className="event-highlights">
      {execution?.durability !== undefined && <span>Durability <b>{durabilityLabel(execution.durability)}</b></span>}
      {execution?.finalAttempt !== undefined && <span>Final attempt <b>{String(execution.finalAttempt)}</b></span>}
      {typeof failure?.message === 'string' && failure.message && (
        <span className="failure-message">{failure.message}</span>
      )}
    </div>
  );
}
