import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

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

function NodeRow({nodes}: {nodes: NodeProps[]}): ReactNode {
  return (
    <div className="exec-row">
      {nodes.map((node) => (
        <DiagramNode key={node.title} {...node} />
      ))}
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

type Copy = {
  label: string;
  client: NodeProps;
  afterClient: NodeProps;
  callArrow?: string;
  workers?: NodeProps[];
  workerArrow?: string;
  stack?: {node: NodeProps; after?: string}[];
  panels?: {title: string; tags: string[]}[];
  apis: NodeProps[];
  apiArrow?: string;
  problems?: string[];
};

const EN: Record<ExecutionApproach, Copy> = {
  naive: {
    label: 'Direct API calls without durable execution',
    client: {
      kicker: 'CLIENT',
      title: 'Frontend API',
      detail: 'Returns after both calls finish — or fails halfway.',
    },
    afterClient: {
      kicker: 'SERVICE',
      title: 'Order handler',
      detail: 'charge(); ship(); in one request thread.',
    },
    callArrow: 'sequential calls',
    apis: [{title: 'Charge API', compact: true}, {title: 'Ship API', compact: true}],
    problems: [
      'Crash or timeout after charge leaves no durable next step.',
      'Retries can double-charge or double-ship.',
      'Fine for a POC. Breaks in production.',
    ],
  },
  'table-poll': {
    label: 'Database row polling with workers',
    client: {
      kicker: 'CLIENT',
      title: 'Frontend API',
      detail: 'Insert row, return request id.',
    },
    afterClient: {
      kicker: 'STORAGE',
      title: 'Orders table',
      detail: 'status = pending | charging | shipping | done',
      tone: 'accent',
    },
    callArrow: 'poll / claim rows',
    workers: [
      {kicker: 'WORKER POOL', title: 'Worker A', detail: 'SELECT … FOR UPDATE', compact: true},
      {title: 'Worker B', detail: 'same table scan', compact: true, tone: 'warn'},
    ],
    apis: [{title: 'Charge API', compact: true}, {title: 'Ship API', compact: true}],
    problems: [
      'Who claims work — locks, skip-locked scans, or shard columns?',
      'Worker dies mid-row: who puts the task back on the queue?',
      'Every new step adds columns, status values, and recovery jobs.',
    ],
  },
  'message-queue': {
    label: 'Message queues between charge and ship',
    client: {
      kicker: 'CLIENT',
      title: 'Frontend API',
      detail: 'Publish to charge queue, return early.',
    },
    afterClient: {kicker: 'QUEUE 1', title: 'charge-requests', tone: 'accent'},
    stack: [
      {node: {title: 'Charge consumer', detail: 'Call charge API, publish to ship queue.'}},
      {
        node: {kicker: 'QUEUE 2', title: 'ship-requests', tone: 'accent'},
        after: 'no cross-queue transaction',
      },
      {node: {title: 'Ship consumer', detail: 'Call ship API, maybe write status row.'}},
    ],
    apis: [],
    problems: [
      'Queue 2 can succeed while queue 1 times out — both steps run twice.',
      'Step 3 means queue 3 and another consumer fleet.',
      'Backoff, DLQ, saga rollback, and approval waits become more queues and cron.',
    ],
  },
  dex: {
    label: 'Dex Flow with durable Steps',
    client: {
      kicker: 'CLIENT',
      title: 'Frontend API',
      detail: 'Start Flow; wait for Step 1 via Client API.',
    },
    afterClient: {
      kicker: 'DEX',
      title: 'Order Flow',
      detail: 'ChargeStep → ShipStep (wait / execute) → RefundStep',
      tone: 'success',
    },
    panels: [
      {
        title: 'Durable control',
        tags: ['atomic Step moves', 'per-Step retry', 'timer + channel wait', 'RPC + rollback'],
      },
      {
        title: 'Operations',
        tags: ['step I/O in Dex Web', 'manual replay', 'indexed Attributes'],
      },
    ],
    apiArrow: 'Worker calls external APIs',
    apis: [{title: 'Charge API', compact: true}, {title: 'Ship API', compact: true}],
  },
};

const ZH: Record<ExecutionApproach, Copy> = {
  naive: {
    label: '业务代码直接串行调用 API，没有 Durable Execution',
    client: {
      kicker: 'CLIENT',
      title: '前端 API',
      detail: '两次调用都完成后才返回——或者半路失败。',
    },
    afterClient: {
      kicker: 'SERVICE',
      title: '订单处理',
      detail: '同一个请求线程里 charge(); ship();',
    },
    callArrow: '串行调用',
    apis: [{title: '扣款 API', compact: true}, {title: '发货 API', compact: true}],
    problems: [
      '扣款成功后进程崩溃或超时，下一步没有可恢复的记录。',
      '重试可能重复扣款或重复发货。',
      '做 POC 够用，上生产不行。',
    ],
  },
  'table-poll': {
    label: '数据库行 + Worker 轮询',
    client: {
      kicker: 'CLIENT',
      title: '前端 API',
      detail: '插入一行，返回 request id。',
    },
    afterClient: {
      kicker: 'STORAGE',
      title: '订单表',
      detail: 'status = pending | charging | shipping | done',
      tone: 'accent',
    },
    callArrow: '轮询 / 抢占行',
    workers: [
      {kicker: 'WORKER POOL', title: 'Worker A', detail: 'SELECT … FOR UPDATE', compact: true},
      {title: 'Worker B', detail: '扫同一张表', compact: true, tone: 'warn'},
    ],
    apis: [{title: '扣款 API', compact: true}, {title: '发货 API', compact: true}],
    problems: [
      '谁来领任务？分布式锁、skip locked，还是按分片扫表？',
      'Worker 领了任务没做完，谁把这条 started 的记录放回队列？',
      '每加一步就要加列、加状态、再写一套恢复逻辑。',
    ],
  },
  'message-queue': {
    label: '扣款和发货之间用消息队列衔接',
    client: {
      kicker: 'CLIENT',
      title: '前端 API',
      detail: '写入扣款队列后立刻返回。',
    },
    afterClient: {kicker: 'QUEUE 1', title: 'charge-requests', tone: 'accent'},
    stack: [
      {node: {title: '扣款 consumer', detail: '调扣款 API，再写入发货队列。'}},
      {
        node: {kicker: 'QUEUE 2', title: 'ship-requests', tone: 'accent'},
        after: '队列之间没有事务',
      },
      {node: {title: '发货 consumer', detail: '调发货 API，也许再写一行状态。'}},
    ],
    apis: [],
    problems: [
      '第二个队列写入成功、第一个超时，重试时两步会同时跑。',
      '第三步就要第三套队列和另一批 consumer。',
      'Backoff、DLQ、SAGA 回滚、审批等待，往往又是更多队列和 cron。',
    ],
  },
  dex: {
    label: '用 Dex Flow 把 Step 做成 durable',
    client: {
      kicker: 'CLIENT',
      title: '前端 API',
      detail: 'Start Flow；用 Client API 等待 Step 1。',
    },
    afterClient: {
      kicker: 'DEX',
      title: 'Order Flow',
      detail: 'ChargeStep → ShipStep（等待 / 执行）→ RefundStep',
      tone: 'success',
    },
    panels: [
      {
        title: 'Durable 控制流',
        tags: ['Step 切换是原子的', '每个 Step 自己的 retry', 'Timer + Channel 等待', 'RPC + 回滚'],
      },
      {
        title: '运维',
        tags: ['Dex Web 看每步输入输出', '失败后手动重放', '可检索的 Attribute'],
      },
    ],
    apiArrow: 'Worker 调用外部 API',
    apis: [{title: '扣款 API', compact: true}, {title: '发货 API', compact: true}],
  },
};

function ApproachDiagram({copy}: {copy: Copy}): ReactNode {
  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <DiagramNode {...copy.client} />
      <Arrow />
      <DiagramNode {...copy.afterClient} />
      {copy.panels ? (
        <div className="exec-split">
          {copy.panels.map((panel) => (
            <article className="exec-panel" key={panel.title}>
              <h4>{panel.title}</h4>
              <div className="exec-tags">
                {panel.tags.map((tag) => (
                  <span key={tag}>{tag}</span>
                ))}
              </div>
            </article>
          ))}
        </div>
      ) : null}
      {copy.callArrow ? <Arrow label={copy.callArrow} /> : copy.workers || copy.stack ? <Arrow /> : null}
      {copy.workers ? <NodeRow nodes={copy.workers} /> : null}
      {copy.stack?.map((item) => (
        <React.Fragment key={item.node.title}>
          {item.after ? <Arrow label={item.after} /> : <Arrow />}
          <DiagramNode {...item.node} />
        </React.Fragment>
      ))}
      {copy.apiArrow ? <Arrow label={copy.apiArrow} /> : copy.apis.length > 0 ? <Arrow /> : null}
      {copy.apis.length > 0 ? <NodeRow nodes={copy.apis} /> : null}
      {copy.problems ? <ProblemList items={copy.problems} /> : null}
    </div>
  );
}

export default function ExecutionApproachDiagram({
  approach,
}: {
  approach: ExecutionApproach;
}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = (i18n.currentLocale === 'zh-Hans' ? ZH : EN)[approach];
  return <ApproachDiagram copy={copy} />;
}
