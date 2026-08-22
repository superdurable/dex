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
  application: string;
  request: string;
  dispatch: string;
  result: string;
  response: string;
  client: string;
  server: string;
  worker: string;
};

const EN: Copy = {
  label: 'An RPC travels between the Client and Worker in an Application through Dex Server',
  application: 'Application',
  request: '1  Invoke RPC request',
  dispatch: '2  Route request to Worker',
  result: '3  Return result to Dex Server',
  response: '4  Return output to Client',
  client: 'Client',
  server: 'Dex Server',
  worker: 'Worker',
};

const ZH: Copy = {
  label: 'RPC 在 Application 中的 Client 和 Worker 之间经 Dex Server 传递',
  application: 'Application',
  request: '1  发起 RPC 请求',
  dispatch: '2  将请求发送给 Worker',
  result: '3  将结果返回给 Dex Server',
  response: '4  将输出返回给 Client',
  client: 'Client',
  server: 'Dex Server',
  worker: 'Worker',
};

function Node({
  title,
  x,
  y,
  width,
  height,
  fill = 'var(--surface-solid)',
  textFill = 'var(--text)',
}: {
  title: string;
  x: number;
  y: number;
  width: number;
  height: number;
  fill?: string;
  textFill?: string;
}): ReactNode {
  return (
    <g>
      <rect x={x} y={y} width={width} height={height} rx="14" fill={fill} stroke="var(--line-strong)" />
      <text x={x + width / 2} y={y + height / 2 + 7} fill={textFill} fontSize="19" fontWeight="700" textAnchor="middle">
        {title}
      </text>
    </g>
  );
}

function DexServerNode({title}: {title: string}): ReactNode {
  return (
    <g>
      <rect x="570" y="100" width="260" height="145" rx="14" fill="var(--lime-soft)" stroke="var(--line-strong)" />
      <text x="700" y="148" fill="var(--text)" fontSize="19" fontWeight="700" textAnchor="middle">
        {title}
      </text>
      <path
        d="M 678 180 C 678 175 688 171 700 171 C 712 171 722 175 722 180 V 208 C 722 213 712 217 700 217 C 688 217 678 213 678 208 Z"
        fill="var(--surface-solid)"
        stroke="var(--forest)"
        strokeWidth="2"
      />
      <ellipse cx="700" cy="180" rx="22" ry="9" fill="var(--surface-solid)" stroke="var(--forest)" strokeWidth="2" />
      <path d="M 678 194 C 678 199 688 203 700 203 C 712 203 722 199 722 194" fill="none" stroke="var(--forest)" strokeWidth="2" />
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
        strokeWidth="3"
      />
      <text
        x={(from + to) / 2}
        y={labelY}
        fill="var(--forest)"
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
        viewBox="0 0 880 310"
        preserveAspectRatio="xMidYMid meet"
        style={{display: 'block', height: 'auto', maxWidth: '100%', position: 'relative', width: '100%', zIndex: 1}}>
        <defs>
          <marker
            id="rpc-arrowhead"
            markerHeight="10"
            markerUnits="userSpaceOnUse"
            markerWidth="10"
            orient="auto"
            refX="9"
            refY="5"
            viewBox="0 0 10 10">
            <path d="M 1 1 L 9 5 L 1 9 Z" fill="var(--forest)" />
          </marker>
        </defs>
        <rect
          x="20"
          y="25"
          width="370"
          height="260"
          rx="18"
          fill="var(--surface)"
          stroke="var(--line-strong)"
          strokeDasharray="7 6"
        />
        <text
          x="48"
          y="55"
          fill="var(--forest)"
          fontFamily="var(--ifm-font-family-monospace)"
          fontSize="12"
          fontWeight="650"
          letterSpacing="1.4">
          {copy.application.toUpperCase()}
        </text>
        <Arrow from={360} to={560} y={105} label={copy.request} labelY={91} />
        <Arrow from={560} to={360} y={207} label={copy.dispatch} labelY={193} />
        <Arrow from={360} to={560} y={239} label={copy.result} labelY={264} />
        <Arrow from={560} to={360} y={137} label={copy.response} labelY={162} />
        <Node title={copy.client} x={50} y={75} width={300} height={75} />
        <Node
          title={copy.worker}
          x={50}
          y={180}
          width={300}
          height={75}
          fill="var(--lime-soft)"
          textFill="var(--forest)"
        />
        <DexServerNode title={copy.server} />
      </svg>
    </div>
  );
}
