import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

function TimerIcon(): ReactNode {
  return (
    <svg aria-hidden="true" className="flow-graph-condition-icon" viewBox="0 0 16 16">
      <circle cx="8" cy="8" r="5.5" />
      <path d="M8 4.8v3.5l2.4 1.4" />
    </svg>
  );
}

function ChannelIcon(): ReactNode {
  return (
    <svg aria-hidden="true" className="flow-graph-condition-icon" viewBox="0 0 16 16">
      <circle cx="4" cy="4" r="1.7" />
      <circle cx="12" cy="8" r="1.7" />
      <circle cx="4" cy="12" r="1.7" />
      <path d="M5.7 4.7 10.3 7M5.7 11.3 10.3 9" />
    </svg>
  );
}

function GraphArrow(): ReactNode {
  return (
    <svg className="flow-graph-arrow" viewBox="0 0 18 28" aria-hidden="true">
      <path d="M9 1v17" fill="none" />
      <path d="M3.2 16.2 9 26.2 14.8 16.2z" stroke="none" />
    </svg>
  );
}

type Copy = {
  label: string;
  charge: string;
  ship: string;
  timer: string;
  channel: string;
  shipped: string;
  refund: string;
};

const EN: Copy = {
  label: 'Order processing Flow: charge, wait for seller, ship or refund',
  charge: 'Charge the buyer',
  ship: 'Ship the item',
  timer: '24h',
  channel: 'seller-ok',
  shipped: 'shipped',
  refund: 'Refund the buyer',
};

const ZH: Copy = {
  label: '订单处理 Flow：扣款、等卖家、发货或退款',
  charge: '给买家扣款',
  ship: '发货',
  timer: '24h',
  channel: 'seller-ok',
  shipped: '已发货',
  refund: '给买家退款',
};

function CompactCard({
  kicker,
  title,
  detail,
  tone,
}: {
  kicker: string;
  title: string;
  detail?: string;
  tone?: 'source' | 'done';
}): ReactNode {
  return (
    <div className={`flow-graph-card flow-graph-card-compact${tone ? ` flow-graph-card-${tone}` : ''}`}>
      <span>{kicker}</span>
      <b>{title}</b>
      {detail ? <small>{detail}</small> : null}
    </div>
  );
}

function StepCard({
  name,
  execute,
  waitFor,
  tone,
}: {
  name: string;
  execute: string;
  waitFor?: {timer: string; channel: string};
  tone?: 'waiting' | 'failed';
}): ReactNode {
  return (
    <div className={`flow-graph-card${tone ? ` flow-graph-card-${tone}` : ''}`}>
      <div className="flow-graph-heading">
        <span>STEP</span>
        <b>{name}</b>
      </div>
      <div className={`flow-graph-methods${waitFor ? '' : ' flow-graph-methods-execute-only'}`}>
        {waitFor ? (
          <div className="flow-graph-method flow-graph-method-waitfor">
            <span>WaitFor</span>
            <small className="flow-graph-conditions">
              <i>
                <TimerIcon />
                {waitFor.timer}
              </i>
              <i>
                <ChannelIcon />
                <span>{waitFor.channel}</span>
              </i>
            </small>
          </div>
        ) : null}
        <div className="flow-graph-method flow-graph-method-execute">
          <span>Execute</span>
          <strong>{execute}</strong>
        </div>
      </div>
    </div>
  );
}

export default function OrderProcessingDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <div className="flow-graph">
        <CompactCard kicker="FLOW" title="Start" tone="source" />
        <GraphArrow />
        <StepCard name="ChargeStep" execute={copy.charge} />
        <GraphArrow />
        <StepCard
          name="ShipStep"
          execute={copy.ship}
          waitFor={{timer: copy.timer, channel: copy.channel}}
          tone="waiting"
        />
        <div className="flow-graph-split">
          <div className="flow-graph-split-stem" aria-hidden="true" />
          <div className="flow-graph-split-bar" aria-hidden="true" />
          <div className="flow-graph-split-cards">
            <div className="flow-graph-split-branch">
              <GraphArrow />
              <CompactCard kicker="DONE" title="complete" detail={copy.shipped} tone="done" />
            </div>
            <div className="flow-graph-split-branch">
              <GraphArrow />
              <StepCard name="RefundStep" execute={copy.refund} tone="failed" />
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
