/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode, useRef} from 'react';
import clsx from 'clsx';
import {useThemeConfig} from '@docusaurus/theme-common';
import {splitNavbarItems} from '@docusaurus/theme-common/internal';
import {translate} from '@docusaurus/Translate';
import NavbarItem, {type Props as NavbarItemConfig} from '@theme/NavbarItem';
import {useDismissibleDetails} from '@site/src/components/useDismissibleDetails';

type HeaderNavbarItem = ReturnType<typeof useThemeConfig>['navbar']['items'][number];

// The shared Super Durable header row. Items come from themeConfig, so navbar.json translates them.
export default function NavbarContent(): ReactNode {
  const {navbar} = useThemeConfig();
  const [siteItems, actionItems] = splitNavbarItems(navbar.items);
  const mobileMenuRef = useRef<HTMLDetailsElement>(null);
  useDismissibleDetails(mobileMenuRef);

  return (
    <div className="sd-header-inner">
      <a
        className="sd-wordmark"
        aria-label={translate({
          id: 'shell.header.homeAriaLabel',
          message: 'Super Durable home',
          description: 'The accessible name of the header wordmark link',
        })}
        href={navbar.logo?.href}>
        <BrandSymbol />
        <span>{navbar.title}</span>
      </a>
      <nav
        className="sd-nav"
        aria-label={translate({
          id: 'shell.header.primaryNavigationAriaLabel',
          message: 'Primary navigation',
          description: 'The ARIA label of the header site links',
        })}>
        {siteItems.map((item) => (
          <HeaderLink key={String(item.label)} item={item} />
        ))}
      </nav>
      <div className="sd-header-actions">
        {actionItems.map((item) => (
          <HeaderAction key={String(item.label ?? item.type)} item={item} isMobile={false} />
        ))}
      </div>
      <details className="sd-mobile-menu" ref={mobileMenuRef}>
        <summary>
          {translate({
            id: 'shell.header.menu',
            message: 'Menu',
            description: 'The visible label of the header mobile menu',
          })}
        </summary>
        <nav
          aria-label={translate({
            id: 'shell.header.mobileNavigationAriaLabel',
            message: 'Mobile navigation',
            description: 'The ARIA label of the header mobile menu links',
          })}>
          {siteItems.map((item) => (
            <HeaderLink key={String(item.label)} item={item} />
          ))}
          {actionItems.map((item) => (
            <HeaderAction key={String(item.label ?? item.type)} item={item} isMobile />
          ))}
        </nav>
      </details>
    </div>
  );
}

function HeaderLink({item, className}: {item: HeaderNavbarItem; className?: string}): ReactNode {
  return (
    <a
      className={className}
      href={String(item.href)}
      aria-current={item['aria-current'] === 'page' ? 'page' : undefined}>
      {item.label}
    </a>
  );
}

// Desktop shows quiet links and small buttons; the mobile menu shows plain links and full-width buttons.
function HeaderAction({item, isMobile}: {item: HeaderNavbarItem; isMobile: boolean}): ReactNode {
  if (item.type === 'localeDropdown') {
    return <NavbarItem {...(item as NavbarItemConfig)} mobile={isMobile} />;
  }
  const buttonClassName = typeof item.className === 'string' ? item.className : undefined;
  if (buttonClassName !== undefined) {
    return <HeaderLink item={item} className={clsx(buttonClassName, !isMobile && 'sd-button-small')} />;
  }
  return <HeaderLink item={item} className={isMobile ? undefined : 'sd-quiet-link'} />;
}

function BrandSymbol(): ReactNode {
  return (
    <svg className="brand-symbol" aria-hidden="true" focusable="false" viewBox="0 0 64 64" width="28" height="28">
      <path d="M 13.5 12 V 52" fill="none" stroke="var(--brand-track)" strokeLinecap="round" strokeWidth="12" />
      <path
        d="M 30.5 12 L 50.5 32 L 30.5 52"
        fill="none"
        stroke="var(--brand-head)"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="12"
      />
    </svg>
  );
}
