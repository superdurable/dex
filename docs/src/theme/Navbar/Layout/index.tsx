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
import {ThemeClassNames} from '@docusaurus/theme-common';
import {
  useLockBodyScroll,
  useNavbarMobileSidebar,
  useNavbarSecondaryMenu,
} from '@docusaurus/theme-common/internal';
import {translate} from '@docusaurus/Translate';
import IconClose from '@theme/Icon/Close';
import IconMenu from '@theme/Icon/Menu';
import type {Props} from '@theme/Navbar/Layout';

const DOCS_SIDEBAR_DRAWER_ID = 'docs-sidebar-drawer';

export default function NavbarLayout({children}: Props): ReactNode {
  const mobileSidebar = useNavbarMobileSidebar();
  const toggleRef = useRef<HTMLButtonElement>(null);
  const drawerLabel = translate({
    id: 'shell.docsSidebar.toggleLabel',
    message: 'Docs menu',
    description: 'The label of the button that opens the docs sidebar on narrow screens',
  });

  useEffect(() => {
    if (!mobileSidebar.shown) {
      return undefined;
    }
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        mobileSidebar.toggle();
        toggleRef.current?.focus();
      }
    };
    document.addEventListener('keydown', closeOnEscape);
    return () => document.removeEventListener('keydown', closeOnEscape);
  }, [mobileSidebar]);

  return (
    <>
      {/* Keeps the navbar class: Docusaurus measures .navbar for the TOC highlight. */}
      <header className={clsx(ThemeClassNames.layout.navbar.container, 'navbar', 'sd-header')}>{children}</header>
      <div className="sd-docs-sidebar-bar">
        <button
          ref={toggleRef}
          type="button"
          className="clean-btn sd-docs-sidebar-toggle"
          aria-controls={DOCS_SIDEBAR_DRAWER_ID}
          aria-expanded={mobileSidebar.shown}
          onClick={mobileSidebar.toggle}>
          <IconMenu width={20} height={20} aria-hidden="true" />
          {drawerLabel}
        </button>
      </div>
      {/* Outside the header, whose backdrop-filter would clip fixed descendants. */}
      <div className={clsx('sd-docs-sidebar-drawer', {'navbar-sidebar--show': mobileSidebar.shown})}>
        <div role="presentation" className="navbar-sidebar__backdrop" onClick={mobileSidebar.toggle} />
        <div
          id={DOCS_SIDEBAR_DRAWER_ID}
          role="dialog"
          aria-modal="true"
          aria-label={drawerLabel}
          className={clsx(ThemeClassNames.layout.navbar.mobileSidebar.container, 'navbar-sidebar')}>
          <DocsSidebarDrawerContent label={drawerLabel} />
        </div>
      </div>
    </>
  );
}

function DocsSidebarDrawerContent({label}: {label: string}): ReactNode {
  const mobileSidebar = useNavbarMobileSidebar();
  const secondaryMenu = useNavbarSecondaryMenu();
  useLockBodyScroll(mobileSidebar.shown);
  if (!mobileSidebar.shouldRender) {
    return null;
  }
  return (
    <>
      <div className="navbar-sidebar__brand sd-docs-sidebar-drawer-header">
        <span>{label}</span>
        <button
          type="button"
          aria-label={translate({
            id: 'theme.docs.sidebar.closeSidebarButtonAriaLabel',
            message: 'Close navigation bar',
            description: 'The ARIA label for close button of mobile sidebar',
          })}
          className="clean-btn navbar-sidebar__close"
          onClick={mobileSidebar.toggle}>
          <IconClose color="var(--ifm-color-emphasis-600)" />
        </button>
      </div>
      <div className="navbar-sidebar__items">
        <div className={clsx(ThemeClassNames.layout.navbar.mobileSidebar.panel, 'navbar-sidebar__item menu')}>
          {secondaryMenu.content}
        </div>
      </div>
    </>
  );
}
