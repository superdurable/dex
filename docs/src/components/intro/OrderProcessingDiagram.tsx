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

function Arrow({label}: {label?: string}): ReactNode {
  return (
    <div className="exec-arrow" aria-hidden="true">
      {label ? <span>{label}</span> : null}
    </div>
  );
}

type Copy = {
  label: string;
  charge: string;
  ship: string;
  waits: string[];
  shipped: string;
  refund: string;
};

const EN: Copy = {
  label: 'Order processing Flow: charge, wait for seller, ship or refund',
  charge: 'Charge the buyer. Step-level retry.',
  ship: 'WaitFor seller Channel or Timer',
  waits: [
    'Timer → reminder, loop wait',
    'approval → Execute ship',
    'ship retry exhausted → RefundStep',
  ],
  shipped: 'shipped',
  refund: 'ship retry exhausted',
};

const ZH: Copy = {
  label: '订单处理 Flow：扣款、等卖家、发货或退款',
  charge: '给买家扣款。Step 级 retry。',
  ship: 'WaitFor 卖家 Channel 或 Timer',
  waits: [
    'Timer → 提醒，循环等待',
    '批准 → Execute ship',
    '发货 retry 用尽 → RefundStep',
  ],
  shipped: '已发货',
  refund: '发货 retry 用尽',
};

export default function OrderProcessingDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <DiagramNode kicker="FLOW" title="Start" />
      <Arrow />
      <DiagramNode kicker="STEP" title="ChargeStep" detail={copy.charge} tone="accent" />
      <Arrow />
      <div className="exec-node exec-node-accent">
        <span className="exec-kicker">STEP</span>
        <strong>ShipStep</strong>
        <p>{copy.ship}</p>
        <ul className="exec-list">
          {copy.waits.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
      </div>
      <Arrow />
      <div className="exec-split exec-flow-end">
        <DiagramNode kicker="DONE" title="complete" detail={copy.shipped} tone="success" />
        <DiagramNode kicker="STEP" title="RefundStep" detail={copy.refund} tone="warn" />
      </div>
    </div>
  );
}
