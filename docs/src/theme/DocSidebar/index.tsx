/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode, useEffect, useLayoutEffect, useMemo} from 'react';
import DocSidebar from '@theme-original/DocSidebar';
import {usePluginData} from '@docusaurus/useGlobalData';
import ExecutionEnvironment from '@docusaurus/ExecutionEnvironment';
import {useActiveDocContext, useActivePlugin} from '@docusaurus/plugin-content-docs/client';
import type {PropSidebar, PropSidebarItem} from '@docusaurus/plugin-content-docs';
import type {Props} from '@theme/DocSidebar';
import {markSidebarHydrated} from '@site/src/components/sidebarState';

type SharedDocsSidebarData = {
  mainSidebar: PropSidebar;
  references: Record<string, Record<string, PropSidebar>>;
};

const referencePluginPrefix = 'reference-';
const desktopSidebarScrollerSelector = '.theme-doc-sidebar-container nav.menu';

// Survives the remount that happens when a link crosses into another docs instance.
let desktopSidebarScrollTop: number | undefined;

const useIsomorphicLayoutEffect = ExecutionEnvironment.canUseDOM ? useLayoutEffect : useEffect;

const versionSelectorItem: PropSidebarItem = {
  type: 'html',
  value: '',
  className: 'reference-version-selector-item',
  defaultStyle: false,
};

function expandReferenceEntries(
  items: PropSidebar,
  references: SharedDocsSidebarData['references'],
  activeComponent: string | undefined,
  activeVersionName: string | undefined,
): PropSidebar {
  return items.map((item): PropSidebarItem => {
    if (item.type === 'category') {
      return {
        ...item,
        items: expandReferenceEntries(item.items, references, activeComponent, activeVersionName),
      };
    }
    const component = item.customProps?.referenceComponent;
    if (item.type !== 'link' || typeof component !== 'string') {
      return item;
    }
    const isActiveComponent = component === activeComponent;
    const versionName = isActiveComponent && activeVersionName ? activeVersionName : 'current';
    const referenceItems = references[component]?.[versionName] ?? references[component]?.current ?? [];
    return {
      type: 'category',
      label: item.label,
      collapsible: true,
      collapsed: true,
      customProps: item.customProps,
      items: isActiveComponent ? [versionSelectorItem, ...referenceItems] : referenceItems,
    };
  });
}

function useSharedDocsSidebar(): PropSidebar {
  const {mainSidebar, references} = usePluginData('shared-docs-sidebar') as SharedDocsSidebarData;
  const {pluginId} = useActivePlugin({failfast: true})!;
  const {activeVersion} = useActiveDocContext(pluginId);
  const activeComponent = pluginId.startsWith(referencePluginPrefix)
    ? pluginId.slice(referencePluginPrefix.length)
    : undefined;
  const activeVersionName = activeComponent ? activeVersion?.name : undefined;
  return useMemo(
    () => expandReferenceEntries(mainSidebar, references, activeComponent, activeVersionName),
    [mainSidebar, references, activeComponent, activeVersionName],
  );
}

// On the first page of a visit, bring the current page's link into view.
function revealActiveSidebarLink(scroller: HTMLElement): void {
  const activeLink = scroller.querySelector<HTMLElement>('a[aria-current="page"]');
  if (!activeLink) {
    return;
  }
  const scrollerBox = scroller.getBoundingClientRect();
  const linkBox = activeLink.getBoundingClientRect();
  if (linkBox.top < scrollerBox.top || linkBox.bottom > scrollerBox.bottom) {
    scroller.scrollTop += linkBox.top - scrollerBox.top - scrollerBox.height / 3;
  }
}

function useKeepDesktopSidebarScroll(): void {
  useIsomorphicLayoutEffect(() => {
    const scroller = document.querySelector<HTMLElement>(desktopSidebarScrollerSelector);
    if (!scroller) {
      return undefined;
    }
    if (desktopSidebarScrollTop !== undefined) {
      scroller.scrollTop = desktopSidebarScrollTop;
    } else {
      revealActiveSidebarLink(scroller);
    }
    const rememberScroll = () => {
      desktopSidebarScrollTop = scroller.scrollTop;
    };
    scroller.addEventListener('scroll', rememberScroll, {passive: true});
    return () => scroller.removeEventListener('scroll', rememberScroll);
  }, []);
}

/** Shows the same docs sidebar on every docs instance, including the Developer Reference. */
export default function SharedDocSidebar(props: Props): ReactNode {
  const sidebar = useSharedDocsSidebar();
  useKeepDesktopSidebarScroll();
  useEffect(() => markSidebarHydrated(), []);
  return <DocSidebar {...props} sidebar={sidebar} />;
}
