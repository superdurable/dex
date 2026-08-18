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

function DatabaseIcon(): ReactNode {
  return (
    <svg className="exec-db-icon" viewBox="0 0 32 32" aria-hidden="true">
      <ellipse cx="16" cy="7" rx="11" ry="4.5" fill="none" stroke="currentColor" strokeWidth="1.8" />
      <path
        d="M5 7v18c0 2.5 4.9 4.5 11 4.5s11-2 11-4.5V7"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.8"
      />
      <path
        d="M5 13c0 2.5 4.9 4.5 11 4.5s11-2 11-4.5"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.8"
      />
      <path
        d="M5 19c0 2.5 4.9 4.5 11 4.5s11-2 11-4.5"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.8"
      />
    </svg>
  );
}

type Copy = {
  label: string;
  app: string;
  client: string;
  worker: string;
  toServer: string;
  fromServer: string;
  persist: string;
};

const EN: Copy = {
  label: 'Client and Worker talk to Dex Server through the Dex SDK',
  app: 'APPLICATION',
  client: 'Start and interact with Flow instances.',
  worker: 'Hosts the Flow. Runs Step and RPC implementations.',
  toServer: 'startFlow / invokeRPC',
  fromServer: 'invoke Step / RPC',
  persist: 'Persists Flow instances and execution.',
};

const ZH: Copy = {
  label: 'Client 和 Worker 通过 Dex SDK 与 Dex Server 交互',
  app: '应用',
  client: 'Start Flow 实例，并与之交互。',
  worker: '托管 Flow。跑 Step 和 RPC implementation。',
  toServer: 'startFlow / invokeRPC',
  fromServer: 'invoke Step / RPC',
  persist: '持久化 Flow instance 和 execution。',
};

export default function DexSystemDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <div className="exec-system">
        <div className="exec-app">
          <span className="exec-kicker">{copy.app}</span>
          <div className="exec-app-body">
            <div className="exec-app-roles">
              <DiagramNode kicker="CLIENT" title="Client" detail={copy.client} compact />
              <DiagramNode kicker="WORKER" title="Worker" detail={copy.worker} compact />
            </div>
            <DiagramNode kicker="SDK" title="Dex SDK" compact tone="accent" />
          </div>
        </div>
        <div className="exec-system-arrows">
          <div className="exec-system-arrow exec-system-arrow-to">
            <span>{copy.toServer}</span>
            <i aria-hidden="true" />
          </div>
          <div className="exec-system-arrow exec-system-arrow-from">
            <i aria-hidden="true" />
            <span>{copy.fromServer}</span>
          </div>
        </div>
        <div className="exec-node exec-server">
          <strong>Dex Server</strong>
          <div className="exec-server-store">
            <DatabaseIcon />
            <p>{copy.persist}</p>
          </div>
        </div>
      </div>
    </div>
  );
}
