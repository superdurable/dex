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
  flowName: string;
  queue: string;
  exampleRPC: string;
  waitFor: string;
  waitForDetail: string;
  execute: string;
  condition: string;
};

const EN: Copy = {
  label: 'Draining external Channel publishing: queueChannel enters the Flow, exampleRPC crosses the Flow boundary in both directions, ProcessMessage waits for a message and executes, then conditionally completes or loops back.',
  flowName: 'DrainingExternalChannelFlow',
  queue: 'queueChannel',
  exampleRPC: 'exampleRPC',
  waitFor: 'WaitFor',
  waitForDetail: 'queueChannel.ForOne()',
  execute: 'Execute',
  condition: 'ForceComplete\nIfChannelsEmpty',
};

const ZH: Copy = {
  label: '排空外部 Channel 发布：queueChannel 单向进入 Flow，exampleRPC 双向跨越 Flow 边界，ProcessMessage 等待并处理一条消息，随后条件完成或回环。',
  flowName: 'DrainingExternalChannelFlow',
  queue: 'queueChannel',
  exampleRPC: 'exampleRPC',
  waitFor: 'WaitFor',
  waitForDetail: 'queueChannel.ForOne()',
  execute: 'Execute',
  condition: 'ForceComplete\nIfChannelsEmpty',
};

export default function DrainingChannelForExternalPublishingDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <svg
      aria-label={copy.label}
      className="draining-channel-lucid-graph"
      role="img"
      viewBox="0 0 1120 800"
      xmlns="http://www.w3.org/2000/svg">
      <title>{copy.label}</title>
      <defs>
        <marker id="draining-channel-arrow" markerHeight="9" markerWidth="9" orient="auto" refX="8" refY="4.5">
          <path d="M0,0 L9,4.5 L0,9 Z" fill="#313c45" />
        </marker>
      </defs>

      <rect fill="#f5f1fb" height="720" rx="16" stroke="#856bb7" strokeWidth="2" width="610" x="300" y="40" />
      <text fill="#4a3869" fontFamily="Inter, Arial, sans-serif" fontSize="14" fontWeight="800" letterSpacing="1.8" x="334" y="78">
        FLOW
      </text>
      <text fill="#28213a" fontFamily="Inter, Arial, sans-serif" fontSize="25" fontWeight="700" x="334" y="112">
        {copy.flowName}
      </text>

      <path d="M92 230 H272 L320 270 L272 310 H92 Z" fill="#e3fae3" stroke="#518b5d" strokeWidth="2" />
      <text fill="#25452c" fontFamily="Inter, Arial, sans-serif" fontSize="19" fontWeight="700" textAnchor="middle" x="190" y="278">
        {copy.queue}
      </text>

      <path d="M78 595 L118 555 H274 L274 540 L320 580 L274 620 L274 605 H118 Z" fill="#c3f7c8" stroke="#518b5d" strokeWidth="2" />
      <text fill="#25452c" fontFamily="Inter, Arial, sans-serif" fontSize="19" fontWeight="700" textAnchor="middle" x="195" y="587">
        {copy.exampleRPC}
      </text>

      <rect fill="#edf5ff" height="255" rx="10" stroke="#6d8fbd" strokeWidth="2" width="405" x="400" y="180" />
      <text fill="#28425d" fontFamily="Inter, Arial, sans-serif" fontSize="24" fontWeight="700" textAnchor="middle" x="602" y="220">
        ProcessMessage
      </text>
      <path d="M423 263 H573 L603 323 L573 383 H423 L450 323 Z" fill="#fff3d9" stroke="#d39732" strokeWidth="2" />
      <text fill="#664718" fontFamily="Inter, Arial, sans-serif" fontSize="19" fontWeight="700" textAnchor="middle" x="512" y="310">
        {copy.waitFor}
      </text>
      <text fill="#664718" fontFamily="Inter, Arial, sans-serif" fontSize="14" textAnchor="middle" x="512" y="338">
        {copy.waitForDetail}
      </text>
      <rect fill="#c3f7c8" height="106" rx="8" stroke="#54a95e" strokeWidth="2" width="153" x="626" y="270" />
      <text fill="#25452c" fontFamily="Inter, Arial, sans-serif" fontSize="20" fontWeight="700" textAnchor="middle" x="702" y="330">
        {copy.execute}
      </text>

      <path d="M602 435 V492" fill="none" markerEnd="url(#draining-channel-arrow)" stroke="#313c45" strokeWidth="2" />
      <path d="M602 495 L700 575 L602 655 L504 575 Z" fill="#dedeff" stroke="#635dff" strokeWidth="2" />
      {copy.condition.split('\n').map((line, index) => (
        <text
          fill="#363075"
          fontFamily="Inter, Arial, sans-serif"
          fontSize="18"
          fontWeight="700"
          key={line}
          textAnchor="middle"
          x="602"
          y={568 + index * 25}>
          {line}
        </text>
      ))}

      <path
        d="M700 575 H815 Q840 575 840 550 V338 Q840 323 805 323"
        fill="none"
        markerEnd="url(#draining-channel-arrow)"
        stroke="#313c45"
        strokeWidth="2"
      />
    </svg>
  );
}
