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
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import {useAlternatePageUtils} from '@docusaurus/theme-common/internal';
import {useLocation} from '@docusaurus/router';
import {translate} from '@docusaurus/Translate';
import type {Props} from '@theme/NavbarItem/LocaleDropdownNavbarItem';
import {ChevronDown, Globe} from 'lucide-react';
import {isDocsLocale, persistDocsLocale} from '@site/src/components/docsLocale';
import {useDismissibleDetails} from '@site/src/components/useDismissibleDetails';

// The header locale switch: a labelled dropdown at the end of the tab row, a row of links in the mobile menu.
export default function LocaleDropdownNavbarItem({mobile, queryString = ''}: Props): ReactNode {
  const {i18n: {currentLocale, locales, localeConfigs}} = useDocusaurusContext();
  const alternatePageUtils = useAlternatePageUtils();
  const {search, hash} = useLocation();
  const detailsRef = useRef<HTMLDetailsElement>(null);
  useDismissibleDetails(detailsRef);
  const languageLabel = translate({
    id: 'shell.header.languageAriaLabel',
    message: 'Language',
    description: 'The ARIA label of the header locale switch',
  });
  const currentLocaleLabel = localeConfigs[currentLocale]!.label;

  // Full page loads: each locale is a separate build.
  const localeLinks = locales.map((locale) => {
    const persistLocale = () => {
      if (isDocsLocale(locale)) {
        persistDocsLocale(locale);
      }
    };
    return (
      <a
        key={locale}
        href={`${alternatePageUtils.createUrl({locale, fullyQualified: false})}${search}${hash}${queryString}`}
        lang={localeConfigs[locale]!.htmlLang}
        aria-current={locale === currentLocale ? 'true' : undefined}
        onClick={persistLocale}
        onAuxClick={persistLocale}>
        {localeConfigs[locale]!.label}
      </a>
    );
  });

  if (mobile) {
    return (
      <div className="sd-locale-row" role="group" aria-label={languageLabel}>
        {localeLinks}
      </div>
    );
  }
  return (
    <details className="sd-locale" ref={detailsRef}>
      <summary className="sd-locale-summary" aria-label={`${languageLabel}: ${currentLocaleLabel}`}>
        <Globe size={16} strokeWidth={2} aria-hidden="true" />
        <span>{currentLocaleLabel}</span>
        <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
      </summary>
      <div className="sd-locale-menu">{localeLinks}</div>
    </details>
  );
}
