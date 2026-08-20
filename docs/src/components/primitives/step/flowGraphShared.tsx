import React, {type ReactNode} from 'react';

export function GraphArrow({label}: {label?: string}): ReactNode {
  return (
    <div className="flow-graph-arrow-wrap">
      {label ? <span className="flow-graph-arrow-label">{label}</span> : null}
      <svg className="flow-graph-arrow" viewBox="0 0 18 28" aria-hidden="true">
        <path d="M9 1v17" fill="none" />
        <path d="M3.2 16.2 9 26.2 14.8 16.2z" stroke="none" />
      </svg>
    </div>
  );
}

export function GraphLoopBack(): ReactNode {
  return (
    <svg
      className="flow-graph-loop-def-connector"
      viewBox="0 0 56 172"
      preserveAspectRatio="none"
      aria-hidden="true">
      <path d="M 2 150 L 36 150 Q 48 150 48 138 L 48 34 Q 48 22 36 22 L 2 22" fill="none" />
      <path d="M 11 17 L 2 22 L 11 27 Z" stroke="none" />
    </svg>
  );
}

export function TimerIcon(): ReactNode {
  return (
    <svg aria-hidden="true" className="flow-graph-condition-icon" viewBox="0 0 16 16">
      <circle cx="8" cy="8" r="5.5" />
      <path d="M8 4.8v3.5l2.4 1.4" />
    </svg>
  );
}

export function ChannelIcon(): ReactNode {
  return (
    <svg aria-hidden="true" className="flow-graph-condition-icon" viewBox="0 0 16 16">
      <circle cx="4" cy="4" r="1.7" />
      <circle cx="12" cy="8" r="1.7" />
      <circle cx="4" cy="12" r="1.7" />
      <path d="M5.7 4.7 10.3 7M5.7 11.3 10.3 9" />
    </svg>
  );
}

export function CompactCard({
  kicker,
  title,
  detail,
  tone,
}: {
  kicker: string;
  title: string;
  detail?: string;
  tone?: 'source' | 'done' | 'waiting' | 'failed' | 'decision';
}): ReactNode {
  return (
    <div className={`flow-graph-card flow-graph-card-compact${tone ? ` flow-graph-card-${tone}` : ''}`}>
      <span>{kicker}</span>
      <b>{title}</b>
      {detail ? <small>{detail}</small> : null}
    </div>
  );
}

export function StepCard({
  name,
  execute,
  waitFor,
  executeOnly,
  waitForOnly,
  tone,
}: {
  name: string;
  execute?: string;
  waitFor?: {timer?: string; channel?: string; skip?: string};
  executeOnly?: boolean;
  waitForOnly?: boolean;
  tone?: 'waiting' | 'failed';
}): ReactNode {
  const showWaitFor = !executeOnly && waitFor;
  const showExecute = !waitForOnly && execute;
  return (
    <div className={`flow-graph-card${tone ? ` flow-graph-card-${tone}` : ''}`}>
      <div className="flow-graph-heading">
        <span>STEP</span>
        <b>{name}</b>
      </div>
      <div
        className={`flow-graph-methods${
          showWaitFor && showExecute ? '' : ' flow-graph-methods-execute-only'
        }`}>
        {showWaitFor ? (
          <div className="flow-graph-method flow-graph-method-waitfor">
            <span>WaitFor</span>
            <small className="flow-graph-conditions">
              {waitFor.skip ? <i>{waitFor.skip}</i> : null}
              {waitFor.timer ? (
                <i>
                  <TimerIcon />
                  {waitFor.timer}
                </i>
              ) : null}
              {waitFor.channel ? (
                <i>
                  <ChannelIcon />
                  <span>{waitFor.channel}</span>
                </i>
              ) : null}
            </small>
          </div>
        ) : null}
        {showExecute ? (
          <div className="flow-graph-method flow-graph-method-execute">
            <span>Execute</span>
            <strong>{execute}</strong>
          </div>
        ) : null}
      </div>
    </div>
  );
}
