import React, {type ReactNode} from 'react';

export type ExecutionApproach = 'naive' | 'table-poll' | 'message-queue' | 'dex';

type NodeProps = {
  kicker?: string;
  title: string;
  detail?: string;
  tone?: 'neutral' | 'accent' | 'warn' | 'success';
  compact?: boolean;
};

function DiagramNode({kicker, title, detail, tone = 'neutral', compact}: NodeProps): ReactNode {
  return (
    <div className={`exec-node exec-node-${tone}${compact ? ' exec-node-compact' : ''}`}>
      {kicker ? <span className="exec-kicker">{kicker}</span> : null}
      <strong>{title}</strong>
      {detail ? <p>{detail}</p> : null}
    </div>
  );
}

function Arrow({label}: {label?: string}): ReactNode {
  return (
    <div className="exec-arrow" aria-hidden="true">
      {label ? <span>{label}</span> : null}
    </div>
  );
}

function ProblemList({items}: {items: string[]}): ReactNode {
  return (
    <ul className="exec-problems">
      {items.map((item) => (
        <li key={item}>{item}</li>
      ))}
    </ul>
  );
}

function NaiveDiagram(): ReactNode {
  return (
    <div className="exec-diagram" role="img" aria-label="Direct API calls without durable execution">
      <DiagramNode kicker="CLIENT" title="Frontend API" detail="Returns after both calls finish — or fails halfway." />
      <Arrow />
      <DiagramNode kicker="SERVICE" title="Order handler" detail="charge(); ship(); in one request thread." />
      <Arrow label="sequential calls" />
      <div className="exec-row">
        <DiagramNode title="Charge API" compact />
        <DiagramNode title="Ship API" compact />
      </div>
      <ProblemList
        items={[
          'Crash or timeout after charge leaves no durable next step.',
          'Retries can double-charge or double-ship.',
          'Fine for a POC. Breaks in production.',
        ]}
      />
    </div>
  );
}

function TablePollDiagram(): ReactNode {
  return (
    <div className="exec-diagram" role="img" aria-label="Database row polling with workers">
      <DiagramNode kicker="CLIENT" title="Frontend API" detail="Insert row, return request id." />
      <Arrow />
      <DiagramNode kicker="STORAGE" title="Orders table" detail="status = pending | charging | shipping | done" tone="accent" />
      <Arrow label="poll / claim rows" />
      <div className="exec-row">
        <DiagramNode kicker="WORKER POOL" title="Worker A" detail="SELECT … FOR UPDATE" compact />
        <DiagramNode title="Worker B" detail="same table scan" compact tone="warn" />
      </div>
      <Arrow />
      <div className="exec-row">
        <DiagramNode title="Charge API" compact />
        <DiagramNode title="Ship API" compact />
      </div>
      <ProblemList
        items={[
          'Who claims work — locks, skip-locked scans, or shard columns?',
          'Worker dies mid-row: who puts the task back on the queue?',
          'Every new step adds columns, status values, and recovery jobs.',
        ]}
      />
    </div>
  );
}

function MessageQueueDiagram(): ReactNode {
  return (
    <div className="exec-diagram" role="img" aria-label="Message queues between charge and ship">
      <DiagramNode kicker="CLIENT" title="Frontend API" detail="Publish to charge queue, return early." />
      <Arrow />
      <DiagramNode kicker="QUEUE 1" title="charge-requests" tone="accent" />
      <Arrow />
      <DiagramNode title="Charge consumer" detail="Call charge API, publish to ship queue." />
      <Arrow label="no cross-queue transaction" />
      <DiagramNode kicker="QUEUE 2" title="ship-requests" tone="accent" />
      <Arrow />
      <DiagramNode title="Ship consumer" detail="Call ship API, maybe write status row." />
      <ProblemList
        items={[
          'Queue 2 can succeed while queue 1 times out — both steps run twice.',
          'Step 3 means queue 3 and another consumer fleet.',
          'Backoff, DLQ, saga rollback, and approval waits become more queues and cron.',
        ]}
      />
    </div>
  );
}

function DexDiagram(): ReactNode {
  return (
    <div className="exec-diagram" role="img" aria-label="Dex Flow with durable Steps">
      <DiagramNode kicker="CLIENT" title="Frontend API" detail="Start Flow; wait for Step 1 via Client API." />
      <Arrow />
      <DiagramNode
        kicker="DEX"
        title="Order Flow"
        detail="ChargeStep → ApprovalStep → ShipStep"
        tone="success"
      />
      <div className="exec-split">
        <article className="exec-panel">
          <h4>Durable control</h4>
          <div className="exec-tags">
            <span>atomic Step moves</span>
            <span>per-Step retry</span>
            <span>timer + channel wait</span>
            <span>RPC + rollback</span>
          </div>
        </article>
        <article className="exec-panel">
          <h4>Operations</h4>
          <div className="exec-tags">
            <span>step I/O in Dex Web</span>
            <span>manual replay</span>
            <span>indexed Attributes</span>
          </div>
        </article>
      </div>
      <Arrow label="Worker calls external APIs" />
      <div className="exec-row">
        <DiagramNode title="Charge API" compact />
        <DiagramNode title="Ship API" compact />
      </div>
    </div>
  );
}

const DIAGRAMS: Record<ExecutionApproach, () => ReactNode> = {
  naive: NaiveDiagram,
  'table-poll': TablePollDiagram,
  'message-queue': MessageQueueDiagram,
  dex: DexDiagram,
};

export default function ExecutionApproachDiagram({
  approach,
}: {
  approach: ExecutionApproach;
}): ReactNode {
  const Diagram = DIAGRAMS[approach];
  return <Diagram />;
}
