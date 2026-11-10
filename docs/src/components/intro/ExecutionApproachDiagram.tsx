// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import {
  TriangleAlert,
  ArrowDown,
  Cog,
  CreditCard,
  Cpu,
  Database,
  Inbox,
  MonitorSmartphone,
  Server,
  Truck,
  Undo2,
  type LucideIcon,
} from 'lucide-react';

import styles from './ComparedToDatabase.module.css';

export type ExecutionApproach = 'naive' | 'table-poll' | 'message-queue' | 'dex';

type NodeProps = {
  kicker?: string;
  title: string;
  detail?: string;
  icon: LucideIcon;
  tone?: 'neutral' | 'accent' | 'warn' | 'success';
  compact?: boolean;
};

const toneClassNames = {
  neutral: '',
  accent: styles.nodeAccent,
  warn: styles.nodeWarn,
  success: styles.nodeSuccess,
};

function DiagramNode({kicker, title, detail, icon: Icon, tone = 'neutral', compact}: NodeProps): ReactNode {
  const className = [
    styles.node,
    toneClassNames[tone],
    compact ? styles.nodeCompact : '',
    compact && detail ? styles.nodeCompactDetail : '',
  ]
    .filter(Boolean)
    .join(' ');
  return (
    <div className={className}>
      <span className={styles.nodeIcon}>
        <Icon className={styles.icon} aria-hidden="true" />
      </span>
      <span className={styles.nodeText}>
        {kicker ? <span className={styles.kicker}>{kicker}</span> : null}
        <strong className={styles.nodeTitle}>{title}</strong>
        {detail ? <p className={styles.nodeDetail}>{detail}</p> : null}
      </span>
    </div>
  );
}

function Arrow({label}: {label?: string}): ReactNode {
  return (
    <div className={styles.arrow} aria-hidden="true">
      <ArrowDown className={styles.icon} />
      {label ? <span>{label}</span> : null}
    </div>
  );
}

function NodeRow({nodes}: {nodes: NodeProps[]}): ReactNode {
  return (
    <div className={styles.row}>
      {nodes.map((node) => (
        <DiagramNode key={node.title} {...node} />
      ))}
    </div>
  );
}

function ProblemList({items}: {items: string[]}): ReactNode {
  return (
    <ul className={styles.problems}>
      {items.map((item) => (
        <li key={item}>
          <TriangleAlert className={styles.icon} aria-hidden="true" />
          <span>{item}</span>
        </li>
      ))}
    </ul>
  );
}

type Copy = {
  client: NodeProps;
  afterClient: NodeProps;
  tags?: string[];
  callArrow?: string;
  workers?: NodeProps[];
  stack?: {node: NodeProps; after?: string}[];
  apis?: NodeProps[];
  apiArrow?: string;
  problems?: string[];
};

const EN: Record<ExecutionApproach, Copy> = {
  naive: {
    client: {
      icon: MonitorSmartphone,
      kicker: 'CLIENT',
      title: 'Frontend API',
      detail: 'Returns after both calls finish — or fails halfway.',
    },
    afterClient: {
      icon: Cog,
      kicker: 'SERVICE',
      title: 'Order handler',
      detail: 'charge(); ship(); in one request thread.',
    },
    problems: [
      'Crash or timeout after charge leaves corrupted state',
      'Fine for a POC. Breaks in production.',
    ],
  },
  'table-poll': {
    client: {
      icon: MonitorSmartphone,
      kicker: 'CLIENT',
      title: 'Frontend API',
      detail: 'Insert row, return request id.',
    },
    afterClient: {
      icon: Database,
      kicker: 'STORAGE',
      title: 'Orders table',
      detail: 'status = pending | charging | shipping | done',
      tone: 'accent',
    },
    callArrow: 'poll / claim rows',
    workers: [
      {icon: Cpu, kicker: 'WORKER POOL', title: 'Worker A', detail: 'SELECT … FOR UPDATE', compact: true},
      {icon: Cpu, title: 'Worker B', detail: 'same table scan', compact: true, tone: 'warn'},
    ],
  },
  'message-queue': {
    client: {
      icon: MonitorSmartphone,
      kicker: 'CLIENT',
      title: 'Frontend API',
      detail: 'Publish to charge queue, return early.',
    },
    afterClient: {icon: Inbox, kicker: 'QUEUE 1', title: 'charge-requests', tone: 'accent'},
    stack: [
      {node: {icon: Cpu, title: 'Charge consumer', detail: 'Call charge API, publish to ship queue.'}},
      {
        node: {icon: Inbox, kicker: 'QUEUE 2', title: 'ship-requests', tone: 'accent'},
        after: 'no cross-queue transaction',
      },
      {node: {icon: Cpu, title: 'Ship consumer', detail: 'Call ship API, maybe write status row.'}},
    ],
    problems: [
      'The ship message is sent, but the charge ack times out: charge and ship both run twice.',
      'Step 3 means queue 3 and another consumer fleet.',
      'Backoff, DLQ, saga rollback, and approval waits become more queues and cron.',
    ],
  },
  dex: {
    client: {
      icon: MonitorSmartphone,
      kicker: 'CLIENT',
      title: 'Frontend API',
      detail: 'Starts the Flow, then waits for the Charge Step with the Client API.',
    },
    afterClient: {
      icon: Server,
      kicker: 'DEX',
      title: 'Dex Server',
      detail: 'Records each Step result and the move to the next Step together.',
      tone: 'success',
    },
    tags: ['Retries per Step', 'Durable Timers', 'Waits on Channels', 'Step history and rerun'],
    callArrow: 'sends each Step task to one Worker',
    workers: [
      {icon: Cpu, kicker: 'YOUR WORKERS', title: 'Worker A', detail: 'Charge, Ship, Refund Steps', compact: true},
      {icon: Cpu, title: 'Worker B', detail: 'Same Flow code', compact: true},
    ],
    apiArrow: 'Steps call the APIs',
    apis: [
      {icon: CreditCard, title: 'Charge API', compact: true},
      {icon: Truck, title: 'Ship API', compact: true},
      {icon: Undo2, title: 'Refund API', compact: true},
    ],
  },
};

const ZH: Record<ExecutionApproach, Copy> = {
  naive: {
    client: {
      icon: MonitorSmartphone,
      kicker: 'CLIENT',
      title: '前端 API',
      detail: '两次调用都完成后才返回——或者半路失败。',
    },
    afterClient: {
      icon: Cog,
      kicker: 'SERVICE',
      title: '订单处理',
      detail: '同一个请求线程里 charge(); ship();',
    },
    problems: [
      '扣款后崩溃或超时会留下损坏的状态',
      '做 POC 够用，上生产不行。',
    ],
  },
  'table-poll': {
    client: {
      icon: MonitorSmartphone,
      kicker: 'CLIENT',
      title: '前端 API',
      detail: '插入一行，返回 request id。',
    },
    afterClient: {
      icon: Database,
      kicker: 'STORAGE',
      title: '订单表',
      detail: 'status = pending | charging | shipping | done',
      tone: 'accent',
    },
    callArrow: '轮询 / 抢占行',
    workers: [
      {icon: Cpu, kicker: 'WORKER POOL', title: 'Worker A', detail: 'SELECT … FOR UPDATE', compact: true},
      {icon: Cpu, title: 'Worker B', detail: '扫同一张表', compact: true, tone: 'warn'},
    ],
  },
  'message-queue': {
    client: {
      icon: MonitorSmartphone,
      kicker: 'CLIENT',
      title: '前端 API',
      detail: '写入扣款队列后立刻返回。',
    },
    afterClient: {icon: Inbox, kicker: 'QUEUE 1', title: 'charge-requests', tone: 'accent'},
    stack: [
      {node: {icon: Cpu, title: '扣款消费者', detail: '调扣款 API，再写入发货队列。'}},
      {
        node: {icon: Inbox, kicker: 'QUEUE 2', title: 'ship-requests', tone: 'accent'},
        after: '队列之间没有事务',
      },
      {node: {icon: Cpu, title: '发货消费者', detail: '调发货 API，也许再写一行状态。'}},
    ],
    problems: [
      '发货消息已发出，扣款消息的确认却超时：扣款和发货都会执行两次。',
      '第三步就要第三套队列和另一批消费者。',
      'Backoff、DLQ、SAGA 回滚、审批等待，往往又是更多队列和 cron。',
    ],
  },
  dex: {
    client: {
      icon: MonitorSmartphone,
      kicker: 'CLIENT',
      title: '前端 API',
      detail: '启动 Flow，再用 Client API 等 Charge Step 完成。',
    },
    afterClient: {
      icon: Server,
      kicker: 'DEX',
      title: 'Dex Server',
      detail: '把每个 Step 的结果和到下一个 Step 的推进一起记录。',
      tone: 'success',
    },
    tags: ['每个 Step 自己的 retry', 'Durable Timer', '等待 Channel', 'Step 历史和重新执行'],
    callArrow: '每个 Step 任务只派给一个 Worker',
    workers: [
      {icon: Cpu, kicker: 'YOUR WORKERS', title: 'Worker A', detail: 'Charge、Ship、Refund Step', compact: true},
      {icon: Cpu, title: 'Worker B', detail: '同一份 Flow 代码', compact: true},
    ],
    apiArrow: 'Step 调用外部 API',
    apis: [
      {icon: CreditCard, title: '扣款 API', compact: true},
      {icon: Truck, title: '发货 API', compact: true},
      {icon: Undo2, title: '退款 API', compact: true},
    ],
  },
};

function ApproachDiagram({copy, markdownDescription}: {copy: Copy; markdownDescription: string}): ReactNode {
  return (
    <div className={`${styles.palette} ${styles.approach}`} role="img" aria-label={markdownDescription}>
      <DiagramNode {...copy.client} />
      <Arrow />
      <DiagramNode {...copy.afterClient} />
      {copy.tags ? (
        <div className={styles.tags}>
          {copy.tags.map((tag) => (
            <span key={tag} className={`${styles.chip} ${styles.chipDex}`}>
              {tag}
            </span>
          ))}
        </div>
      ) : null}
      {copy.workers ? <Arrow label={copy.callArrow} /> : null}
      {copy.workers ? <NodeRow nodes={copy.workers} /> : null}
      {copy.stack?.map((item) => (
        <React.Fragment key={item.node.title}>
          {item.after ? <Arrow label={item.after} /> : <Arrow />}
          <DiagramNode {...item.node} />
        </React.Fragment>
      ))}
      {copy.apis && copy.apis.length > 0 ? (
        <>
          {copy.apiArrow ? <Arrow label={copy.apiArrow} /> : <Arrow />}
          <NodeRow nodes={copy.apis} />
        </>
      ) : null}
      {copy.problems && copy.problems.length > 0 ? <ProblemList items={copy.problems} /> : null}
    </div>
  );
}

export default function ExecutionApproachDiagram({
  approach,
  markdownDescription,
}: {
  approach: ExecutionApproach;
  markdownDescription: string;
}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = (i18n.currentLocale === 'zh-Hans' ? ZH : EN)[approach];
  return <ApproachDiagram copy={copy} markdownDescription={markdownDescription} />;
}
