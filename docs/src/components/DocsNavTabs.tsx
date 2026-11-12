/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode, useEffect, useRef} from 'react';
import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useBaseUrl from '@docusaurus/useBaseUrl';
import {useLocation} from '@docusaurus/router';
import {translate} from '@docusaurus/Translate';
import type {LucideIcon} from 'lucide-react';
import {BookOpen, Bot, LayoutGrid, Rocket} from 'lucide-react';
import {useThemeConfig} from '@docusaurus/theme-common';
import NavbarItem, {type Props as NavbarItemConfig} from '@theme/NavbarItem';

type DocsNavTabKey = 'docs' | 'quick-start' | 'agents' | 'examples';

type DocsNavTab = {key: DocsNavTabKey; to: string; icon: LucideIcon; label: string};

// The docs path prefix that selects each tab. Everything else is the Docs tab.
const tabPrefixes: [DocsNavTabKey, string][] = [
  ['quick-start', 'quick-start'],
  ['agents', 'agent-frameworks'],
  ['examples', 'use-cases'],
];

/** Picks the active tab from a docs path, with or without a locale prefix. */
export function activeDocsNavTab(pathname: string, baseUrl: string): DocsNavTabKey {
  const relative = pathname.startsWith(baseUrl) ? pathname.slice(baseUrl.length) : pathname.replace(/^\//, '');
  const firstSegment = relative.split('/')[0] ?? '';
  return tabPrefixes.find(([, prefix]) => prefix === firstSegment)?.[0] ?? 'docs';
}

// A second header row: four tabs that repeat the main entry points of the sidebar, and the language switch.
export default function DocsNavTabs(): ReactNode {
  const {pathname} = useLocation();
  const baseUrl = useBaseUrl('/');
  const active = activeDocsNavTab(pathname, baseUrl);
  const localeItem = useThemeConfig().navbar.items.find((item) => item.type === 'localeDropdown');
  const scrollRef = useRef<HTMLDivElement>(null);
  // On narrow screens the tabs scroll; bring the active one into view without moving the page.
  useEffect(() => {
    const container = scrollRef.current;
    const activeTab = container?.querySelector<HTMLElement>('.sd-tab--active');
    if (!container || !activeTab) {
      return;
    }
    const overflow = activeTab.offsetLeft + activeTab.offsetWidth - (container.scrollLeft + container.clientWidth);
    if (overflow > 0) {
      container.scrollLeft += overflow + 24;
    }
  }, [active]);
  const tabs: DocsNavTab[] = [
    {
      key: 'docs',
      to: '/',
      icon: BookOpen,
      label: translate({id: 'shell.tabs.docs', message: 'Docs', description: 'The header tab for the docs home'}),
    },
    {
      key: 'quick-start',
      to: '/quick-start',
      icon: Rocket,
      label: translate({id: 'shell.tabs.quickStart', message: 'Quickstart', description: 'The header tab for the Quick Start'}),
    },
    {
      key: 'agents',
      to: '/agent-frameworks',
      icon: Bot,
      label: translate({id: 'shell.tabs.agents', message: 'AI Agents', description: 'The header tab for Durable Agents'}),
    },
    {
      key: 'examples',
      to: '/use-cases',
      icon: LayoutGrid,
      label: translate({id: 'shell.tabs.examples', message: 'Examples', description: 'The header tab for the product examples'}),
    },
  ];
  return (
    <nav
      className="sd-tabs"
      aria-label={translate({
        id: 'shell.tabs.ariaLabel',
        message: 'Docs sections',
        description: 'The ARIA label of the header tab row',
      })}>
      <div className="sd-tabs-inner">
        <div className="sd-tabs-scroll" ref={scrollRef}>
          {tabs.map(({key, to, icon: Icon, label}) => (
            <Link
              key={key}
              to={to}
              className={clsx('sd-tab', key === active && 'sd-tab--active')}
              aria-current={key === active ? 'page' : undefined}>
              <Icon size={17} strokeWidth={2} aria-hidden="true" />
              {label}
            </Link>
          ))}
        </div>
        {localeItem ? <NavbarItem {...(localeItem as NavbarItemConfig)} mobile={false} /> : null}
      </div>
    </nav>
  );
}
