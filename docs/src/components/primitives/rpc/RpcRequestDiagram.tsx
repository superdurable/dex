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
  request: 'RPC request',
  dispatch: 'invoke RPC',
  result: 'RPC result',
  response: 'return result',
  client: 'Client',
  server: 'Dex Server',
  worker: 'Worker',
};

const ZH: Copy = {
  label: 'RPC 在 Application 中的 Client 和 Worker 之间经 Dex Server 传递',
  application: 'Application',
  request: 'RPC 请求',
  dispatch: '调用 RPC',
  result: 'RPC 结果',
  response: '返回结果',
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
        stroke="var(--line-strong)"
        strokeLinecap="round"
        strokeWidth="2.5"
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
        viewBox="0 0 880 310"
        preserveAspectRatio="xMidYMid meet"
        style={{display: 'block', height: 'auto', maxWidth: '100%', position: 'relative', width: '100%', zIndex: 1}}>
        <defs>
          <marker
            id="rpc-arrowhead"
            markerHeight="8"
            markerUnits="userSpaceOnUse"
            markerWidth="8"
            orient="auto"
            refX="7"
            refY="4"
            viewBox="0 0 8 8">
            <path d="M 1 1 L 7 4 L 1 7" fill="none" stroke="var(--line-strong)" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.5" />
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
        <Arrow from={350} to={570} y={105} label={copy.request} labelY={91} />
        <Arrow from={570} to={350} y={137} label={copy.response} labelY={162} />
        <Arrow from={570} to={350} y={207} label={copy.dispatch} labelY={193} />
        <Arrow from={350} to={570} y={239} label={copy.result} labelY={264} />
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
        <Node
          title={copy.server}
          fill="var(--lime-soft)"
          x={570}
          y={100}
          width={260}
          height={145}
        />
      </svg>
    </div>
  );
}
