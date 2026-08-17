import React, {type ReactNode} from 'react';
import clsx from 'clsx';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import {useLocation} from '@docusaurus/router';
import {
  type DocsLocale,
  isDocsLocale,
  localizedPath,
  persistDocsLocale,
} from './docsLocale';

const LABELS: Record<DocsLocale, string> = {
  en: 'English',
  'zh-Hans': '中文',
};

export default function LocaleSwitcher({
  className,
}: {
  className?: string;
}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const {pathname, search, hash} = useLocation();
  const currentLocale: DocsLocale = isDocsLocale(i18n.currentLocale)
    ? i18n.currentLocale
    : 'en';

  function switchLocale(locale: DocsLocale) {
    persistDocsLocale(locale);
    if (locale === currentLocale) {
      return;
    }
    const nextPath = localizedPath(locale, pathname);
    window.location.assign(`${nextPath}${search}${hash}`);
  }

  return (
    <div className={clsx('locale-switcher', className)} role="group" aria-label="Language">
      {(['en', 'zh-Hans'] as const).map((locale) => (
        <button
          key={locale}
          className={clsx('locale-switcher-option', locale === currentLocale && 'is-active')}
          type="button"
          lang={locale === 'zh-Hans' ? 'zh-Hans' : 'en'}
          aria-pressed={locale === currentLocale}
          onClick={() => switchLocale(locale)}>
          {LABELS[locale]}
        </button>
      ))}
    </div>
  );
}
