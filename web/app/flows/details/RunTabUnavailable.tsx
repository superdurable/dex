// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ReactNode } from 'react';
import { ExternalLink } from 'lucide-react';
import { DEX_DOCS_URL } from '../../components/frame/SettingsMenu';
import { FLOW_DEFINITION_SOURCES_URL } from '../../rendering/FlowRenderingPage';

export const AGENT_FRAMEWORK_SUPPORT_URL = `${DEX_DOCS_URL}agent-frameworks/framework-support`;
export const DEX_WEB_ACTIONS_URL = `${DEX_DOCS_URL}reference/web/dex-web#actions`;

/** Why a run's Actions tab has nothing to offer. */
export type RunActionsUnavailableReason = 'no-definition' | 'unusable-definition' | 'no-actions';

/** Why a run's Conversation tab has no session to show. */
export type RunConversationUnavailableReason = 'not-a-session' | 'unusable-definition';

/** Explains, in the tab's panel, what makes Conversation work and why this run has none. */
export function RunConversationUnavailable({ reason }: { reason: RunConversationUnavailableReason }) {
  return (
    <RunTabUnavailable title="Conversation" lead="Conversation shows a Dex Agents session: a run of an agent built with a supported agent framework.">
      <p>
        {reason === 'not-a-session'
          ? 'This run is not a Dex Agents session.'
          : 'This run\'s Flow type has no valid Flow Definition Graph 2.0 that the Dex Server can serve, so Dex Web cannot read a session from it.'}
      </p>
      <DocsLinks links={[{ href: AGENT_FRAMEWORK_SUPPORT_URL, label: 'Supported agent frameworks' }]} />
    </RunTabUnavailable>
  );
}

/** Explains, in the tab's panel, what makes Actions work and why this run has none. */
export function RunActionsUnavailable({ flowType, reason }: { flowType: string; reason: RunActionsUnavailableReason }) {
  const detail = {
    'no-definition': `No Flow Definition Graph is loaded for the Flow type ${flowType}.`,
    'unusable-definition': `The loaded definition of ${flowType} is not a valid Flow Definition Graph 2.0, or the Dex Server does not serve it.`,
    'no-actions': `The loaded definition of ${flowType} declares no Action and no Display field.`,
  }[reason];
  const links = reason === 'no-actions'
    ? [{ href: DEX_WEB_ACTIONS_URL, label: 'Actions in Dex Web' }]
    : [{ href: FLOW_DEFINITION_SOURCES_URL, label: 'Load Flow definitions' }, { href: DEX_WEB_ACTIONS_URL, label: 'Actions in Dex Web' }];
  return (
    <RunTabUnavailable title="Actions" lead="Actions lists the RPCs that the Flow type's Flow Definition Graph declares as Actions.">
      <p>{detail}</p>
      <DocsLinks links={links} />
    </RunTabUnavailable>
  );
}

function RunTabUnavailable({ title, lead, children }: { title: string; lead: string; children: ReactNode }) {
  return (
    <section aria-label={`${title} unavailable`} className="card run-tab-unavailable">
      <p className="run-tab-unavailable-lead">{lead}</p>
      {children}
    </section>
  );
}

function DocsLinks({ links }: { links: { href: string; label: string }[] }) {
  return (
    <p className="run-tab-unavailable-links">
      {links.map((link) => (
        <a href={link.href} key={link.href} rel="noreferrer" target="_blank">
          {link.label}
          <ExternalLink aria-hidden="true" size={12} strokeWidth={2.25} />
        </a>
      ))}
    </p>
  );
}
