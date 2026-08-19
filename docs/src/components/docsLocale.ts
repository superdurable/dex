export const DOCS_LOCALES = ['en', 'zh-Hans'] as const;

export type DocsLocale = (typeof DOCS_LOCALES)[number];

export const DEFAULT_DOCS_LOCALE: DocsLocale = 'en';
export const DOCS_LOCALE_STORAGE_KEY = 'docs-locale';
export const DOCS_LOCALE_COOKIE = 'superdurable-docs-locale';

const COOKIE_MAX_AGE = 60 * 60 * 24 * 365;
const ZH_PREFIX = '/zh-Hans';

export function isDocsLocale(value: string | null | undefined): value is DocsLocale {
  return value === 'en' || value === 'zh-Hans';
}

export function persistDocsLocale(locale: DocsLocale): void {
  window.localStorage.setItem(DOCS_LOCALE_STORAGE_KEY, locale);
  const shared =
    window.location.hostname === 'superdurable.io' ||
    window.location.hostname.endsWith('.superdurable.io');
  document.cookie =
    `${DOCS_LOCALE_COOKIE}=${locale}; Path=/; Max-Age=${COOKIE_MAX_AGE}; SameSite=Lax` +
    (shared ? '; Domain=.superdurable.io; Secure' : '');
}

export function pathWithoutLocale(pathname: string): string {
  if (pathname === ZH_PREFIX || pathname === `${ZH_PREFIX}/`) {
    return '/';
  }
  if (pathname.startsWith(`${ZH_PREFIX}/`)) {
    return pathname.slice(ZH_PREFIX.length) || '/';
  }
  return pathname;
}

export function localizedPath(locale: DocsLocale, pathname: string): string {
  const rest = pathWithoutLocale(pathname);
  if (locale === DEFAULT_DOCS_LOCALE) {
    return rest;
  }
  return rest === '/' ? `${ZH_PREFIX}/` : `${ZH_PREFIX}${rest}`;
}
