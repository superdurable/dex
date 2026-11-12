// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  Archive,
  BadgeCheck,
  Bell,
  CalendarClock,
  Clock,
  Cpu,
  CreditCard,
  Database,
  History,
  Inbox,
  RadioTower,
  RefreshCw,
  RotateCcw,
  Send,
  Table2,
  Timer,
  Truck,
  Undo2,
  Wrench,
  type LucideIcon,
} from 'lucide-react';

import type {DocsLocale} from '../docsLocale';

/** One part a design needs, drawn as a chip with an icon. */
export type OrderProcessPart = {
  label: string;
  icon: LucideIcon;
};

/** Parts that share a role, such as tables or queues. */
export type OrderProcessPartGroup = {
  title: string;
  parts: OrderProcessPart[];
};

/** One design of the full order process and the platform it runs on. */
export type OrderProcessDesign = {
  name: string;
  subtitle: string;
  groups: OrderProcessPartGroup[];
  platformTitle: string;
  platformParts: OrderProcessPart[];
};

/** What one design adds for one new requirement. */
export type RequirementChangeSide = {
  newParts: OrderProcessPart[];
  changes: string[];
};

/** One requirement added after the first release. */
export type RequirementChange = {
  title: string;
  icon: LucideIcon;
  queues: RequirementChangeSide;
  dex: RequirementChangeSide;
};

export type OrderProcessCopy = {
  queues: OrderProcessDesign;
  dex: OrderProcessDesign;
  ariaLabel: string;
  queuesColumn: string;
  dexColumn: string;
  requirementChanges: RequirementChange[];
  requirementsAriaLabel: string;
};

const EN: OrderProcessCopy = {
  ariaLabel: 'Parts of the full order process: a database with queues and an outbox, and Dex',
  queues: {
    name: 'Database, queues, and outbox',
    subtitle: 'What you build and run',
    groups: [
      {
        title: 'Tables',
        parts: [
          {label: 'Orders table with status', icon: Database},
          {label: 'Approvals table', icon: Table2},
          {label: 'Outbox table', icon: Table2},
        ],
      },
      {
        title: 'Queues',
        parts: [
          {label: 'charge-requests', icon: Inbox},
          {label: 'ship-requests', icon: Inbox},
          {label: 'refund-requests', icon: Inbox},
          {label: 'Retry topics or visibility timeouts', icon: RefreshCw},
          {label: 'Dead-letter queue (DLQ)', icon: Archive},
        ],
      },
      {
        title: 'Services and jobs',
        parts: [
          {label: 'Charge consumers', icon: Cpu},
          {label: 'Ship consumers', icon: Cpu},
          {label: 'Refund consumers', icon: Cpu},
          {label: 'Outbox relay', icon: Send},
          {label: 'Reminder cron job', icon: CalendarClock},
          {label: 'Approval endpoint', icon: BadgeCheck},
          {label: 'Status polling endpoint', icon: RotateCcw},
          {label: 'DLQ inspect and repair tools', icon: Wrench},
        ],
      },
    ],
    platformTitle: 'Runs on a database and a queue broker',
    platformParts: [],
  },
  dex: {
    name: 'Dex',
    subtitle: 'What you write and run',
    groups: [
      {
        title: 'One Flow',
        parts: [
          {label: 'Charge Step', icon: CreditCard},
          {label: 'Ship Step', icon: Truck},
          {label: 'Refund Step', icon: Undo2},
          {label: 'approve RPC', icon: Send},
        ],
      },
      {
        title: 'Services',
        parts: [{label: 'Workers that host the Flow', icon: Cpu}],
      },
    ],
    platformTitle: 'Runs on Dex Server, which provides',
    platformParts: [
      {label: 'Retries per Step', icon: RefreshCw},
      {label: 'Durable Timers', icon: Timer},
      {label: 'Waits on Channels', icon: RadioTower},
      {label: 'Client wait API', icon: Clock},
      {label: 'Step history and rerun', icon: History},
    ],
  },
  requirementsAriaLabel: 'What each design adds for each new requirement',
  queuesColumn: 'Database, queues, and outbox add',
  dexColumn: 'Dex adds',
  requirementChanges: [
    {
      title: 'Refund the buyer after an hour of ship failures',
      icon: Undo2,
      queues: {
        newParts: [
          {label: 'refund-requests', icon: Inbox},
          {label: 'Refund consumers', icon: Cpu},
        ],
        changes: ['Ship consumers record the first failure and give up after an hour.'],
      },
      dex: {
        newParts: [{label: 'Refund Step', icon: Undo2}],
        changes: ['Ship Step: retry for one hour, then move to the Refund Step.'],
      },
    },
    {
      title: "Wait for the seller's approval before shipping",
      icon: BadgeCheck,
      queues: {
        newParts: [
          {label: 'Approvals table', icon: Table2},
          {label: 'Approval endpoint', icon: BadgeCheck},
        ],
        changes: ['The approval endpoint, not the charge consumer, publishes to ship-requests.'],
      },
      dex: {
        newParts: [{label: 'approve RPC', icon: Send}],
        changes: ['Ship Step waits for a message on the seller Channel.'],
      },
    },
    {
      title: 'Remind the seller',
      icon: Bell,
      queues: {
        newParts: [{label: 'Reminder cron job', icon: CalendarClock}],
        changes: ['The job scans the approvals table for orders that still wait.'],
      },
      dex: {
        newParts: [],
        changes: ['Ship Step also waits for a Timer. When it fires, the Step sends a reminder and waits again.'],
      },
    },
  ],
};

const ZH: OrderProcessCopy = {
  ariaLabel: '完整订单流程需要的部件：数据库、队列加 outbox，以及 Dex',
  queues: {
    name: '数据库、队列加 outbox',
    subtitle: '你要搭建和运行的部分',
    groups: [
      {
        title: '表',
        parts: [
          {label: '带 status 的订单表', icon: Database},
          {label: '审批表', icon: Table2},
          {label: 'outbox 表', icon: Table2},
        ],
      },
      {
        title: '队列',
        parts: [
          {label: 'charge-requests', icon: Inbox},
          {label: 'ship-requests', icon: Inbox},
          {label: 'refund-requests', icon: Inbox},
          {label: 'retry topic 或 visibility timeout', icon: RefreshCw},
          {label: '死信队列（DLQ）', icon: Archive},
        ],
      },
      {
        title: '服务和任务',
        parts: [
          {label: '扣款消费者', icon: Cpu},
          {label: '发货消费者', icon: Cpu},
          {label: '退款消费者', icon: Cpu},
          {label: 'outbox relay', icon: Send},
          {label: '提醒 cron job', icon: CalendarClock},
          {label: '审批接口', icon: BadgeCheck},
          {label: '状态轮询接口', icon: RotateCcw},
          {label: 'DLQ 查看和修复工具', icon: Wrench},
        ],
      },
    ],
    platformTitle: '运行在数据库和队列 broker 上',
    platformParts: [],
  },
  dex: {
    name: 'Dex',
    subtitle: '你要编写和运行的部分',
    groups: [
      {
        title: '一个 Flow',
        parts: [
          {label: 'Charge Step', icon: CreditCard},
          {label: 'Ship Step', icon: Truck},
          {label: 'Refund Step', icon: Undo2},
          {label: 'approve RPC', icon: Send},
        ],
      },
      {
        title: '服务',
        parts: [{label: '运行这个 Flow 的 Worker', icon: Cpu}],
      },
    ],
    platformTitle: '运行在 Dex Server 上，它提供',
    platformParts: [
      {label: '每个 Step 自己的 retry', icon: RefreshCw},
      {label: 'Durable Timer', icon: Timer},
      {label: '等待 Channel', icon: RadioTower},
      {label: 'Client 等待 API', icon: Clock},
      {label: 'Step 历史和重新执行', icon: History},
    ],
  },
  requirementsAriaLabel: '每个新需求在两种设计里各要加什么',
  queuesColumn: '数据库、队列加 outbox 要加',
  dexColumn: 'Dex 要加',
  requirementChanges: [
    {
      title: '发货失败一小时后给买家退款',
      icon: Undo2,
      queues: {
        newParts: [
          {label: 'refund-requests', icon: Inbox},
          {label: '退款消费者', icon: Cpu},
        ],
        changes: ['发货消费者记下第一次失败的时间，一小时后放弃。'],
      },
      dex: {
        newParts: [{label: 'Refund Step', icon: Undo2}],
        changes: ['Ship Step：retry 一小时，然后进入 Refund Step。'],
      },
    },
    {
      title: '发货前等卖家审批',
      icon: BadgeCheck,
      queues: {
        newParts: [
          {label: '审批表', icon: Table2},
          {label: '审批接口', icon: BadgeCheck},
        ],
        changes: ['改由审批接口写入 ship-requests，扣款消费者不再写。'],
      },
      dex: {
        newParts: [{label: 'approve RPC', icon: Send}],
        changes: ['Ship Step 等待卖家 Channel 上的消息。'],
      },
    },
    {
      title: '提醒卖家',
      icon: Bell,
      queues: {
        newParts: [{label: '提醒 cron job', icon: CalendarClock}],
        changes: ['这个 job 扫描审批表，找出还在等的订单。'],
      },
      dex: {
        newParts: [],
        changes: ['Ship Step 同时等一个 Timer。Timer 到了，就发提醒，然后继续等。'],
      },
    },
  ],
};

export function orderProcessCopy(locale: string): OrderProcessCopy {
  return (locale as DocsLocale) === 'zh-Hans' ? ZH : EN;
}
