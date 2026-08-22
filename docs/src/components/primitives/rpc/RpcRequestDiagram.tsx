/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode} from 'react';
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
  x,
  fill = 'var(--surface-solid)',
  textFill = 'var(--text)',
  detailFill = 'var(--muted-strong)',
}: {
  kicker: string;
  title: string;
  detail: string;
  x: number;
  fill?: string;
  textFill?: string;
  detailFill?: string;
}): ReactNode {
  return (
    <g>
      <rect x={x} y="80" width="205" height="120" rx="14" fill={fill} stroke="var(--line-strong)" />
      <text
        x={x + 102.5}
        y="112"
        fill={detailFill}
        fontFamily="var(--ifm-font-family-monospace)"
        fontSize="11"
        fontWeight="650"
        letterSpacing="1.4"
        textAnchor="middle">
        {kicker}
      </text>
      <text x={x + 102.5} y="146" fill={textFill} fontSize="19" fontWeight="700" textAnchor="middle">
        {title}
      </text>
      <text x={x + 102.5} y="174" fill={detailFill} fontSize="13" textAnchor="middle">
        {detail}
      </text>
    </g>
  );
}

function Arrow({
  from,
  to,
  y,
  label,
  labelY,
}: {
  from: number;
  to: number;
  y: number;
  label: string;
  labelY: number;
}): ReactNode {
  return (
    <g>
      <path
        d={`M ${from} ${y} H ${to}`}
        fill="none"
        markerEnd="url(#rpc-arrowhead)"
        stroke="var(--forest)"
        strokeLinecap="round"
        strokeWidth="4"
      />
      <text
        x={(from + to) / 2}
        y={labelY}
        fill="var(--muted-strong)"
        fontSize="12"
        fontWeight="650"
        textAnchor="middle">
        {label}
      </text>
    </g>
  );
}

export default function RpcRequestDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <svg
        viewBox="0 0 880 260"
        preserveAspectRatio="xMidYMid meet"
        style={{display: 'block', height: 'auto', maxWidth: '100%', position: 'relative', width: '100%', zIndex: 1}}>
        <defs>
          <marker
            id="rpc-arrowhead"
            markerHeight="9"
            markerWidth="9"
            orient="auto"
            refX="7.5"
            refY="4.5"
            viewBox="0 0 9 9">
            <path d="M 0 0 L 9 4.5 L 0 9 Z" fill="var(--forest)" />
          </marker>
        </defs>
        <Arrow from={225} to={337} y={112} label={copy.request} labelY={62} />
        <Arrow from={542} to={654} y={112} label={copy.dispatch} labelY={62} />
        <Arrow from={654} to={542} y={172} label={copy.result} labelY={226} />
        <Arrow from={337} to={225} y={172} label={copy.response} labelY={226} />
        <Node kicker="CLIENT" title={copy.client} detail={copy.clientDetail} x={20} />
        <Node kicker="DEX SERVER" title={copy.server} detail={copy.serverDetail} fill="var(--lime-soft)" x={337} />
        <Node
          kicker="WORKER"
          title={copy.worker}
          detail={copy.workerDetail}
          detailFill="#d7f6ab"
          fill="var(--forest)"
          textFill="#ffffff"
          x={654}
        />
      </svg>
    </div>
  );
}
