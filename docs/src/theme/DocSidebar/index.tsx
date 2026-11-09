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
import {useWindowSize} from '@docusaurus/theme-common';
import type {PropSidebar, PropSidebarItem} from '@docusaurus/plugin-content-docs';
import type {Props} from '@theme/DocSidebar';
import {markSidebarHydrated} from '@site/src/components/sidebarState';

type SharedDocsSidebarData = {
  mainSidebar: PropSidebar;
  references: Record<string, Record<string, PropSidebar>>;
};

const referencePluginPrefix = 'reference-';
const desktopSidebarScrollerSelector = '.theme-doc-sidebar-container nav.menu';
// Longer than the Docusaurus Collapsible animation.
const categoryAnimationMs = 400;

// Survives the remount that happens when a link crosses into another docs instance.
let desktopSidebarScrollTop = 0;
// The first desktop sidebar of a visit reveals the current page instead of restoring scroll.
let hasDesktopSidebarMounted = false;

const useIsomorphicLayoutEffect = ExecutionEnvironment.canUseDOM ? useLayoutEffect : useEffect;

function versionSelectorItem(component: string): PropSidebarItem {
  return {
    type: 'html',
    value: '',
    className: 'reference-version-selector-item',
    defaultStyle: false,
    customProps: {referencePluginId: `${referencePluginPrefix}${component}`},
  };
}

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
    const versions = references[component];
    if (!versions) {
      return item;
    }
    const referenceItems = versions[versionName] ?? versions.current ?? [];
    return {
      type: 'category',
      label: item.label,
      collapsible: true,
      collapsed: true,
      customProps: item.customProps,
      items: [versionSelectorItem(component), ...referenceItems],
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
  // The reader already scrolled the sidebar.
  if (!activeLink || scroller.scrollTop !== 0) {
    return;
  }
  const scrollerBox = scroller.getBoundingClientRect();
  const linkBox = activeLink.getBoundingClientRect();
  if (linkBox.top < scrollerBox.top || linkBox.bottom > scrollerBox.bottom) {
    scroller.scrollTop += linkBox.top - scrollerBox.top - scrollerBox.height / 3;
  }
}

function useKeepDesktopSidebarScroll(): void {
  // Docusaurus swaps the desktop and mobile sidebars at the breakpoint, so rerun for a new scroller.
  const windowSize = useWindowSize();
  useIsomorphicLayoutEffect(() => {
    // The first client render reports "ssr" even on a phone; wait for the real breakpoint.
    const scroller = windowSize === 'desktop'
      ? document.querySelector<HTMLElement>(desktopSidebarScrollerSelector)
      : null;
    if (!scroller) {
      return undefined;
    }
    const rememberScroll = () => {
      desktopSidebarScrollTop = scroller.scrollTop;
    };
    if (hasDesktopSidebarMounted) {
      scroller.scrollTop = desktopSidebarScrollTop;
    } else {
      // Saved category state opens categories after the first paint, with a short animation.
      const revealTimer = window.setTimeout(() => {
        hasDesktopSidebarMounted = true;
        revealActiveSidebarLink(scroller);
      }, categoryAnimationMs);
      scroller.addEventListener('scroll', rememberScroll, {passive: true});
      return () => {
        window.clearTimeout(revealTimer);
        scroller.removeEventListener('scroll', rememberScroll);
      };
    }
    scroller.addEventListener('scroll', rememberScroll, {passive: true});
    return () => scroller.removeEventListener('scroll', rememberScroll);
  }, [windowSize]);
}

/** Shows the same docs sidebar on every docs instance, including the Developer Reference. */
export default function SharedDocSidebar(props: Props): ReactNode {
  const sidebar = useSharedDocsSidebar();
  useKeepDesktopSidebarScroll();
  useEffect(() => markSidebarHydrated(), []);
  return <DocSidebar {...props} sidebar={sidebar} />;
}
