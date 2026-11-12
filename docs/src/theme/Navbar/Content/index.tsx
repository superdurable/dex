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
import {useColorMode} from '@docusaurus/theme-common';
import {translate} from '@docusaurus/Translate';
import SearchBar from '@theme/SearchBar';
import {Moon, Sun} from 'lucide-react';
import {persistSharedTheme} from '@site/src/components/BrandThemeToggle';
import DocsNavTabs from '@site/src/components/DocsNavTabs';
import {DEX_CLOUD_SIGN_UP_URL, DEX_DISCORD_URL, DEX_GITHUB_URL, SUPER_DURABLE_HOME_URL} from '@site/src/components/brandNavigation';
import {useDismissibleDetails} from '@site/src/components/useDismissibleDetails';

type HeaderIconLink = {key: string; href: string; label: string; icon: ReactNode};

// Row 1: the wordmark (to superdurable.io), search in the middle, icon-only links, and the Cloud sign-up button.
// Row 2: the four section tabs and the language switch.
export default function NavbarContent(): ReactNode {
  const mobileMenuRef = useRef<HTMLDetailsElement>(null);
  useDismissibleDetails(mobileMenuRef);
  const iconLinks = useHeaderIconLinks();

  return (
    <>
      <div className="sd-header-inner">
        <a
          className="sd-wordmark"
          aria-label={translate({
            id: 'shell.header.superDurableHome',
            message: 'Super Durable home',
            description: 'The header link back to the superdurable.io home page',
          })}
          href={SUPER_DURABLE_HOME_URL}>
          <BrandSymbol />
          <span>
            {translate({id: 'shell.header.docsTitle', message: 'Super Durable', description: 'The header wordmark text'})}
          </span>
        </a>
        <div className="sd-header-search">
          <SearchBar />
        </div>
        <div className="sd-header-actions">
          {iconLinks.map((link) => (
            <a key={link.key} className="sd-icon-link" href={link.href} aria-label={link.label} title={link.label}>
              {link.icon}
            </a>
          ))}
          <HeaderThemeToggle />
          <SignUpCloudButton />
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
            {iconLinks.map((link) => (
              <a key={link.key} className="sd-mobile-icon-link" href={link.href}>
                {link.icon}
                {link.label}
              </a>
            ))}
            <HeaderThemeToggle withLabel />
            <SignUpCloudButton />
          </nav>
        </details>
      </div>
      <DocsNavTabs />
    </>
  );
}

function useHeaderIconLinks(): HeaderIconLink[] {
  return [
    {
      key: 'discord',
      href: DEX_DISCORD_URL,
      label: translate({id: 'shell.header.discord', message: 'Discord', description: 'The header Discord link'}),
      icon: <DiscordIcon />,
    },
    {
      key: 'github',
      href: DEX_GITHUB_URL,
      label: translate({id: 'shell.header.github', message: 'GitHub', description: 'The header GitHub link'}),
      icon: <GitHubIcon />,
    },
  ];
}

function SignUpCloudButton(): ReactNode {
  return (
    <a className="sd-button sd-button-primary sd-button-small sd-signup" href={DEX_CLOUD_SIGN_UP_URL}>
      {translate({id: 'shell.header.signUpCloud', message: 'Sign up Cloud', description: 'The header button to sign up for Dex Cloud'})}
    </a>
  );
}

// Both icons render, and CSS shows the one for the current theme, so the static HTML matches either.
function HeaderThemeToggle({withLabel = false}: {withLabel?: boolean}): ReactNode {
  const {colorMode, setColorMode} = useColorMode();
  const label = translate({
    id: 'shell.header.themeToggle',
    message: 'Switch between light and dark',
    description: 'The accessible name of the header theme switch',
  });
  const toggle = () => {
    const next = colorMode === 'dark' ? 'light' : 'dark';
    setColorMode(next);
    persistSharedTheme(next);
  };
  return (
    <button
      type="button"
      className={withLabel ? 'clean-btn sd-mobile-icon-link' : 'clean-btn sd-icon-link sd-theme-toggle'}
      aria-label={withLabel ? undefined : label}
      title={withLabel ? undefined : label}
      onClick={toggle}>
      <Moon className="sd-theme-icon-moon" size={18} strokeWidth={2} aria-hidden="true" />
      <Sun className="sd-theme-icon-sun" size={18} strokeWidth={2} aria-hidden="true" />
      {withLabel ? label : null}
    </button>
  );
}

function DiscordIcon(): ReactNode {
  return (
    <svg aria-hidden="true" focusable="false" viewBox="0 0 24 24" width="18" height="18">
      <path
        fill="currentColor"
        d="M20.317 4.3698a19.7913 19.7913 0 00-4.8851-1.5152.0741.0741 0 00-.0785.0371c-.211.3753-.4447.8648-.6083 1.2495-1.8447-.2762-3.68-.2762-5.4868 0-.1636-.3933-.4058-.8742-.6177-1.2495a.077.077 0 00-.0785-.037 19.7363 19.7363 0 00-4.8852 1.515.0699.0699 0 00-.0321.0277C.5334 9.0458-.319 13.5799.0992 18.0578a.0824.0824 0 00.0312.0561c2.0528 1.5076 4.0413 2.4228 5.9929 3.0294a.0777.0777 0 00.0842-.0276c.4616-.6304.8731-1.2952 1.226-1.9942a.076.076 0 00-.0416-.1057c-.6528-.2476-1.2743-.5495-1.8722-.8923a.077.077 0 01-.0076-.1277c.1258-.0943.2517-.1923.3718-.2914a.0743.0743 0 01.0776-.0105c3.9278 1.7933 8.18 1.7933 12.0614 0a.0739.0739 0 01.0785.0095c.1202.099.246.1981.3728.2924a.077.077 0 01-.0066.1276 12.2986 12.2986 0 01-1.873.8914.0766.0766 0 00-.0407.1067c.3604.698.7719 1.3628 1.225 1.9932a.076.076 0 00.0842.0286c1.961-.6067 3.9495-1.5219 6.0023-3.0294a.077.077 0 00.0313-.0552c.5004-5.177-.8382-9.6739-3.5485-13.6604a.061.061 0 00-.0312-.0286zM8.02 15.3312c-1.1825 0-2.1569-1.0857-2.1569-2.419 0-1.3332.9555-2.4189 2.157-2.4189 1.2108 0 2.1757 1.0952 2.1568 2.419 0 1.3332-.9555 2.4189-2.1569 2.4189zm7.9748 0c-1.1825 0-2.1569-1.0857-2.1569-2.419 0-1.3332.9554-2.4189 2.1569-2.4189 1.2108 0 2.1757 1.0952 2.1568 2.419 0 1.3332-.946 2.4189-2.1568 2.4189Z"
      />
    </svg>
  );
}

function GitHubIcon(): ReactNode {
  return (
    <svg aria-hidden="true" focusable="false" viewBox="0 0 16 16" width="18" height="18">
      <path
        fill="currentColor"
        d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0 0 16 8c0-4.42-3.58-8-8-8Z"
      />
    </svg>
  );
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
