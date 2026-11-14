// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ReactNode } from 'react';
import { Bot, ExternalLink, MousePointerClick, PencilRuler, type LucideIcon } from 'lucide-react';
import { DEX_DOCS_URL } from '../../components/frame/SettingsMenu';
import { FLOW_DEFINITION_SOURCES_URL } from '../../rendering/FlowRenderingPage';

export const AGENT_FRAMEWORK_SUPPORT_URL = `${DEX_DOCS_URL}agent-frameworks/framework-support`;
export const DEX_WEB_ACTIONS_URL = `${DEX_DOCS_URL}reference/web/dex-web#actions`;
export const FLOW_VISUALIZATION_URL = `${DEX_DOCS_URL}reference/web/flow-visualization`;

/** Why a run's Actions tab has nothing to offer. */
export type RunActionsUnavailableReason = 'no-definition' | 'unusable-definition' | 'no-actions';

/** Why a run's Conversation tab has no session to show. */
export type RunConversationUnavailableReason = 'not-a-session' | 'unusable-definition';

/** Explains, in the tab's panel, what makes Conversation work and why this run has none. */
export function RunConversationUnavailable({ reason }: { reason: RunConversationUnavailableReason }) {
  const why = reason === 'not-a-session'
    ? 'This run is not a Dex Agents session.'
    : 'This Flow type has no valid Flow Definition Graph 2.0 that the Dex Server serves, so no session can be read.';
  return (
    <RunTabUnavailable
      Icon={Bot}
      label="Conversation"
      links={[{ href: AGENT_FRAMEWORK_SUPPORT_URL, label: 'Supported agent frameworks' }]}
      title="No conversation for this run"
    >
      Conversation shows a Dex Agents session: a run of an agent built with a supported agent framework. {why}
    </RunTabUnavailable>
  );
}

/** Explains, in the tab's panel, what makes Actions work and why this run has none. */
export function RunActionsUnavailable({ flowType, reason }: { flowType: string; reason: RunActionsUnavailableReason }) {
  const why = {
    'no-definition': `No Flow Definition Graph is loaded for the Flow type ${flowType}.`,
    'unusable-definition': `The loaded definition of ${flowType} is not a valid Flow Definition Graph 2.0, or the Dex Server does not serve it.`,
    'no-actions': `The loaded definition of ${flowType} declares no Action and no Display field.`,
  }[reason];
  const links = reason === 'no-actions'
    ? [{ href: DEX_WEB_ACTIONS_URL, label: 'Actions in Dex Web' }]
    : [{ href: FLOW_DEFINITION_SOURCES_URL, label: 'Load Flow definitions' }, { href: DEX_WEB_ACTIONS_URL, label: 'Actions in Dex Web' }];
  return (
    <RunTabUnavailable Icon={MousePointerClick} label="Actions" links={links} title="No actions for this run">
      Actions lists the RPCs that the Flow type&apos;s Flow Definition Graph declares as Actions. {why}
    </RunTabUnavailable>
  );
}

/** Explains, in the Events picture, what Design draws and why this run's Flow type has none. */
export function RunDesignUnavailable({ flowType }: { flowType: string }) {
  return (
    <RunTabUnavailable
      Icon={PencilRuler}
      label="Design"
      links={[
        { href: FLOW_DEFINITION_SOURCES_URL, label: 'Load Flow definitions' },
        { href: FLOW_VISUALIZATION_URL, label: 'Generate a Flow Definition Graph' },
      ]}
      title="No design for this Flow type"
    >
      Design draws the Flow type&apos;s Flow Definition Graph, a JSON file generated from the Flow&apos;s source.
      {' '}No usable Flow Definition Graph is loaded for the Flow type {flowType}.
    </RunTabUnavailable>
  );
}

/** A calm empty state: the tab's icon, a short title, the reason, and docs links. */
function RunTabUnavailable({
  Icon,
  label,
  title,
  links,
  children,
}: {
  Icon: LucideIcon;
  label: string;
  title: string;
  links: { href: string; label: string }[];
  children: ReactNode;
}) {
  return (
    <section aria-label={`${label} unavailable`} className="card run-tab-unavailable">
      <div className="run-tab-unavailable-body">
        <Icon aria-hidden="true" className="run-tab-unavailable-icon" size={22} strokeWidth={1.75} />
        <h2 className="run-tab-unavailable-title">{title}</h2>
        <p className="run-tab-unavailable-text">{children}</p>
        <p className="run-tab-unavailable-links">
          {links.map((link) => (
            <a href={link.href} key={link.href} rel="noreferrer" target="_blank">
              {link.label}
              <ExternalLink aria-hidden="true" size={12} strokeWidth={2.25} />
            </a>
          ))}
        </p>
      </div>
    </section>
  );
}
