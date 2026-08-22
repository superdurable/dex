/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type CSSProperties, type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

type Copy = {
  label: string;
  request: string;
  dispatch: string;
  result: string;
  response: string;
  client: string;
  clientDetail: string;
  server: string;
  serverDetail: string;
  worker: string;
  workerDetail: string;
};

const EN: Copy = {
  label: 'RPC request travels from Client to Dex Server to Worker, then the result returns to Client',
  request: 'RPC request',
  dispatch: 'invoke RPC',
  result: 'RPC result',
  response: 'return result',
  client: 'Client',
  clientDetail: 'Invokes an RPC',
  server: 'Dex Server',
  serverDetail: 'Routes the request and result',
  worker: 'Worker',
  workerDetail: 'Runs the RPC method',
};

const ZH: Copy = {
  label: 'RPC 请求从 Client 经 Dex Server 到 Worker，结果再返回 Client',
  request: 'RPC 请求',
  dispatch: '调用 RPC',
  result: 'RPC 结果',
  response: '返回结果',
  client: 'Client',
  clientDetail: '发起 RPC',
  server: 'Dex Server',
  serverDetail: '路由请求和结果',
  worker: 'Worker',
  workerDetail: '执行 RPC method',
};

function Node({
  kicker,
  title,
  detail,
  tone,
}: {
  kicker: string;
  title: string;
  detail: string;
  tone?: 'accent' | 'success';
}): ReactNode {
  return (
    <div className={`exec-node exec-node-compact${tone ? ` exec-node-${tone}` : ''}`}>
      <span className="exec-kicker">{kicker}</span>
      <strong>{title}</strong>
      <p>{detail}</p>
    </div>
  );
}

function Arrow({label, up = false}: {label: string; up?: boolean}): ReactNode {
  const arrowStyle: CSSProperties | undefined = up ? {transform: 'rotate(180deg)'} : undefined;
  const labelStyle: CSSProperties | undefined = up
    ? {transform: 'translateY(-50%) rotate(180deg)'}
    : undefined;
  return (
    <div className="exec-arrow" style={arrowStyle}>
      <span style={labelStyle}>{label}</span>
    </div>
  );
}

export default function RpcRequestDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <Node kicker="CLIENT" title={copy.client} detail={copy.clientDetail} />
      <Arrow label={copy.request} />
      <Node kicker="DEX" title={copy.server} detail={copy.serverDetail} tone="accent" />
      <Arrow label={copy.dispatch} />
      <Node kicker="WORKER" title={copy.worker} detail={copy.workerDetail} tone="success" />
      <Arrow label={copy.result} up />
      <Node kicker="DEX" title={copy.server} detail={copy.serverDetail} tone="accent" />
      <Arrow label={copy.response} up />
      <Node kicker="CLIENT" title={copy.client} detail={copy.clientDetail} />
    </div>
  );
}
