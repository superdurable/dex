// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  createContext, useContext, useEffect, useLayoutEffect, useState, type ReactNode,
} from 'react';
import { createPortal } from 'react-dom';
import {
  ArrowRight, Ban, Check, CircleCheck, CircleDashed, ClockAlert, Copy, KeyRound, TriangleAlert,
  type LucideIcon,
} from 'lucide-react';
import { CONNECTORS_COPY } from './copy';

export type ConnectionStatus = 'Missing' | 'Ready' | 'Expired' | 'Conflict' | 'Unsupported';

const STATUS_ICONS: Record<ConnectionStatus, LucideIcon> = {
  Ready: CircleCheck,
  Expired: ClockAlert,
  Missing: CircleDashed,
  Conflict: TriangleAlert,
  Unsupported: Ban,
};

/** A connection state as an icon and its word; the colour comes from `data-status`. */
export function ConnectionStatusChip({ status }: { status: ConnectionStatus }) {
  const Icon = STATUS_ICONS[status];
  return <span className="connector-status" data-status={status.toLowerCase()}>
    <Icon aria-hidden="true" className="connector-status-icon" size={13} strokeWidth={2.2} />
    {status}
  </span>;
}

/** The first letter or digit of a Connector name, upper-cased, or "?" when it has none. */
export function connectorInitial(name: string): string {
  const match = /[\p{L}\p{N}]/u.exec(name);
  return match ? match[0].toLocaleUpperCase() : '?';
}

/** A brand-neutral initial avatar; colour stays reserved for connection states. */
export function ConnectorAvatar({ name, size = 'md' }: { name: string; size?: 'md' | 'lg' }) {
  return <span aria-hidden="true" className="connector-avatar" data-size={size}>{connectorInitial(name)}</span>;
}

const RELATIVE_TIME_UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 24 * 60 * 60 * 1000],
  ['month', 30 * 24 * 60 * 60 * 1000],
  ['week', 7 * 24 * 60 * 60 * 1000],
  ['day', 24 * 60 * 60 * 1000],
  ['hour', 60 * 60 * 1000],
  ['minute', 60 * 1000],
];

/**
 * Returns the distance from `now` to the ISO timestamp in words, such as "in 3 days" or "2 hours ago".
 * Under a minute reads "now". An unparsable timestamp returns it unchanged.
 */
export function relativeTimeText(iso: string, now: Date): string {
  const time = Date.parse(iso);
  if (Number.isNaN(time)) return iso;
  const distance = time - now.getTime();
  const format = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });
  for (const [unit, size] of RELATIVE_TIME_UNITS) {
    if (Math.abs(distance) >= size) return format.format(Math.sign(distance) * Math.round(Math.abs(distance) / size), unit);
  }
  return 'now';
}

/** Returns the exact local date and time of the ISO timestamp, or the timestamp itself when it does not parse. */
export function exactTimeText(iso: string): string {
  const time = Date.parse(iso);
  if (Number.isNaN(time)) return iso;
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'long' }).format(time);
}

/** Returns the sentence that leads a token expiry: "Token expired" in the past, "Token expires" otherwise. */
export function credentialExpiryLead(iso: string, now: Date): string {
  const time = Date.parse(iso);
  return !Number.isNaN(time) && time <= now.getTime() ? 'Token expired' : 'Token expires';
}

/** The current time, refreshed every half minute so relative times stay true while the page is open. */
export function useNow(intervalMilliseconds = 30_000): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), intervalMilliseconds);
    return () => window.clearInterval(timer);
  }, [intervalMilliseconds]);
  return now;
}

/** "Token expires in 3 days" with the exact time on hover. */
export function CredentialExpiry({ iso, now }: { iso: string; now: Date }) {
  return <span className="connector-expiry">
    {credentialExpiryLead(iso, now)}{' '}
    <time dateTime={iso} title={exactTimeText(iso)}>{relativeTimeText(iso, now)}</time>
  </span>;
}

/** Copies a value; the icon turns into a check once the clipboard holds it. */
export function CopyButton({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 1600);
    return () => window.clearTimeout(timer);
  }, [copied]);
  const Icon = copied ? Check : Copy;
  return <button
    aria-label={`${copied ? 'Copied' : 'Copy'} ${label}`}
    className="v2-ghost connector-copy"
    data-copied={copied || undefined}
    onClick={() => {
      void navigator.clipboard.writeText(value).then(() => setCopied(true));
    }}
    title={copied ? 'Copied' : 'Copy'}
    type="button"
  ><Icon aria-hidden="true" key={copied ? 'copied' : 'copy'} size={14} strokeWidth={2} /></button>;
}

/** A value in monospace followed by its copy button. */
export function CopyValue({ label, value }: { label: string; value: string }) {
  return <span className="connector-copy-value"><code>{value}</code><CopyButton label={label} value={value} /></span>;
}

type NextStepTone = 'expired' | 'missing' | 'conflict' | 'unsupported';

/** The one next step for a connection that is not Ready; a Ready connection renders nothing. */
export function ConnectionNextStep({ connectorName, credentialExpiresAt, now, status, onAct }: {
  connectorName: string;
  credentialExpiresAt?: string;
  now: Date;
  status: ConnectionStatus;
  onAct: () => void;
}) {
  if (status === 'Ready') return null;
  const step = nextStepContent(status, connectorName, credentialExpiresAt, now);
  return <div className="connector-next" data-tone={step.tone}>
    <step.icon aria-hidden="true" className="connector-next-icon" size={18} strokeWidth={2} />
    <div className="connector-next-copy">
      <p className="connector-next-title">{step.title}</p>
      <p className="connector-next-body">{step.body}</p>
    </div>
    {step.action && <button className="v2-primary connector-next-action" onClick={onAct} type="button">
      {step.action}<ArrowRight aria-hidden="true" size={14} strokeWidth={2.2} />
    </button>}
  </div>;
}

function nextStepContent(
  status: Exclude<ConnectionStatus, 'Ready'>,
  connectorName: string,
  credentialExpiresAt: string | undefined,
  now: Date,
): { tone: NextStepTone; icon: LucideIcon; title: ReactNode; body: string; action?: string } {
  switch (status) {
    case 'Expired':
      return {
        tone: 'expired', icon: ClockAlert,
        title: credentialExpiresAt ? <CredentialExpiry iso={credentialExpiresAt} now={now} /> : CONNECTORS_COPY.expiredTitle,
        body: CONNECTORS_COPY.expiredNextStep(connectorName),
        action: CONNECTORS_COPY.reauthorizeAction,
      };
    case 'Missing':
      return {
        tone: 'missing', icon: KeyRound, title: CONNECTORS_COPY.missingTitle,
        body: CONNECTORS_COPY.missingNextStep(connectorName), action: CONNECTORS_COPY.setUpAction,
      };
    case 'Conflict':
      return { tone: 'conflict', icon: TriangleAlert, title: CONNECTORS_COPY.conflictTitle, body: CONNECTORS_COPY.conflict };
    case 'Unsupported':
      return { tone: 'unsupported', icon: Ban, title: CONNECTORS_COPY.unsupportedTitle, body: CONNECTORS_COPY.unsupported };
  }
}

/** Scrolls to the setup form and focuses its first empty required input, else its submit button. */
export function focusConnectionSetup(root: ParentNode = document) {
  const form = root.querySelector<HTMLFormElement>('#connector-host-form');
  const target = form ?? root.querySelector<HTMLElement>('#connector-setup-panel');
  if (!target) return;
  const isReducedMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches === true;
  const field = form
    ? [...form.querySelectorAll<HTMLInputElement>('input[required]')].find((input) => input.value === '')
      ?? form.querySelector<HTMLElement>('button[type="submit"]')
    : null;
  (field ?? target).scrollIntoView({ behavior: isReducedMotion ? 'auto' : 'smooth', block: 'center' });
  field?.focus({ preventScroll: true });
}

/** Detail width, in pixels, from which the side column sits beside the setup. */
export const CONNECTOR_SIDE_COLUMN_MIN_WIDTH = 940;

/** The side column's slot for the authorization guide, or null while the detail is one column. */
export const ConnectorGuideSlotContext = createContext<HTMLElement | null>(null);

/** Returns true while the element is at least `minimumWidth` pixels wide, and false without one. */
export function useIsElementAtLeast(element: HTMLElement | null, minimumWidth: number): boolean {
  const [isAtLeast, setIsAtLeast] = useState(false);
  useLayoutEffect(() => {
    if (!element || typeof ResizeObserver === 'undefined') return;
    const measure = () => setIsAtLeast(element.getBoundingClientRect().width >= minimumWidth);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [element, minimumWidth]);
  return isAtLeast;
}

/** Renders the guide in the side column's slot when there is one, otherwise in place. */
export function InSideColumnGuideSlot({ children }: { children: ReactNode }) {
  const slot = useContext(ConnectorGuideSlotContext);
  return slot ? createPortal(children, slot) : children;
}
