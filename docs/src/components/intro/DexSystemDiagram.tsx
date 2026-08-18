import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

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

type Copy = {
  label: string;
  app: string;
  client: string;
  worker: string;
  toServer: string;
  fromServer: string;
  server: string;
};

const EN: Copy = {
  label: 'Application Client and Worker talk to Dex Server',
  app: 'APPLICATION',
  client: 'Start and interact with Flow instances.',
  worker: 'Hosts the Flow. Runs Step and RPC handlers.',
  toServer: 'start / RPC / wait',
  fromServer: 'dispatch Step / RPC',
  server: 'Durable control. Dispatches tasks to Workers.',
};

const ZH: Copy = {
  label: '应用里的 Client 和 Worker 与 Dex Server 交互',
  app: '应用',
  client: 'Start Flow 实例，并与之交互。',
  worker: '托管 Flow。跑 Step 和 RPC handler。',
  toServer: 'start / RPC / wait',
  fromServer: '派发 Step / RPC',
  server: 'Durable 控制流。把任务派发给 Worker。',
};

export default function DexSystemDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <div className="exec-system">
        <div className="exec-app">
          <span className="exec-kicker">{copy.app}</span>
          <div className="exec-row">
            <DiagramNode kicker="CLIENT" title="Client" detail={copy.client} compact />
            <DiagramNode kicker="WORKER" title="Worker" detail={copy.worker} compact tone="accent" />
          </div>
        </div>
        <div className="exec-system-link">
          <span className="exec-system-link-to">{copy.toServer}</span>
          <div className="exec-system-shaft" aria-hidden="true" />
          <span className="exec-system-link-from">{copy.fromServer}</span>
        </div>
        <DiagramNode kicker="DEX" title="Dex Server" detail={copy.server} tone="success" />
      </div>
    </div>
  );
}
