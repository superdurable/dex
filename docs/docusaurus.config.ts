/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

import {fileURLToPath} from 'node:url';
import clientRedirects from '@docusaurus/plugin-client-redirects';
import {themes as prismThemes} from 'prism-react-renderer';
import type {Config, LoadContext} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';
import redirects from './redirects.json';

// src/theme/Navbar/Content renders left items as the site row and right items as the actions.
const companyHeaderNavigation = {
  sites: [
    {label: 'Dex Engine', href: 'https://superdurable.io/dex-engine/', position: 'left'},
    {label: 'Dex Cloud', href: 'https://superdurable.io/dex-cloud/', position: 'left'},
    {label: 'Dex Connectors', href: 'https://connectors.superdurable.io/', position: 'left'},
    {label: 'Dex Docs', href: 'https://docs.superdurable.io/', position: 'left', 'aria-current': 'page'},
    {label: 'Blog', href: 'https://superdurable.io/blog/', position: 'left'},
    {label: 'About', href: 'https://superdurable.io/about/', position: 'left'},
  ],
  actions: [
    {type: 'localeDropdown', position: 'right'},
    {label: 'GitHub', href: 'https://github.com/superdurable', position: 'right'},
    {label: 'Log in', href: 'https://dex.superdurable.io/login', position: 'right'},
    {label: 'Sign up', href: 'https://dex.superdurable.io/register', position: 'right', className: 'sd-button sd-button-primary'},
  ],
  footer: [
    {
      title: 'Products',
      items: [
        {label: 'Dex Engine', href: 'https://superdurable.io/dex-engine/', target: '_self'},
        {label: 'Dex Cloud', href: 'https://superdurable.io/dex-cloud/', target: '_self'},
        {label: 'Dex Connectors', href: 'https://connectors.superdurable.io/', target: '_self'},
      ],
    },
    {
      title: 'Developers',
      items: [
        {label: 'Dex Docs', to: '/'},
        {label: 'Quick start', to: '/quick-start'},
        {label: 'GitHub', href: 'https://github.com/superdurable', target: '_self'},
        {label: 'Compare engines', href: 'https://superdurable.io/#compare', target: '_self'},
      ],
    },
    {
      title: 'Company',
      items: [
        {label: 'About', href: 'https://superdurable.io/about/', target: '_self'},
        {label: 'Blog', href: 'https://superdurable.io/blog/', target: '_self'},
        {label: 'Contact', href: 'https://superdurable.io/about/#contact', target: '_self'},
      ],
    },
    {
      title: 'Legal',
      items: [
        {label: 'Terms of Service', href: 'https://superdurable.io/terms/', target: '_self'},
        {label: 'Privacy Policy', href: 'https://superdurable.io/privacy/', target: '_self'},
      ],
    },
  ],
} satisfies {sites: NonNullable<Preset.ThemeConfig['navbar']>['items']; actions: NonNullable<Preset.ThemeConfig['navbar']>['items']; footer: NonNullable<Preset.ThemeConfig['footer']>['links']};

const config: Config = {
  title: 'Super Durable Docs',
  tagline: 'Durable Execution for backend engineering',
  // The query makes browsers that cached an earlier icon at this path fetch the new one; bump it when the icon changes.
  favicon: 'img/brand/favicon.svg?v=pipe-d-tile',

  future: {
    v4: true,
  },

  url: 'https://docs.superdurable.io',
  baseUrl: '/',
  trailingSlash: true,

  organizationName: 'superdurable',
  projectName: 'dex',

  onBrokenLinks: 'throw',
  onBrokenAnchors: 'throw',
  onBrokenMarkdownLinks: 'throw',
  onDuplicateRoutes: 'throw',

  headTags: [
    {
      tagName: 'meta',
      attributes: {
        name: 'google-site-verification',
        content: 'sZ7KTJpIynUmNq33gAzRLllx2nJvi2bvTpOYlHc7klg',
      },
    },
    {
      tagName: 'script',
      attributes: {},
      innerHTML: `
(function () {
  try {
    var cookieMatch = document.cookie.match(/(?:^|; )superdurable-theme=(light|dark)(?:;|$)/);
    var cookieTheme = cookieMatch ? cookieMatch[1] : null;
    var storedTheme = window.localStorage.getItem('superdurable-theme');
    var hasStoredTheme = storedTheme === 'dark' || storedTheme === 'light';
    var chosenTheme = cookieTheme || (hasStoredTheme ? storedTheme : null);
    // Docusaurus's color mode script reads 'theme'. Without a shared choice, follow the system.
    if (chosenTheme) {
      window.localStorage.setItem('theme', chosenTheme);
    } else {
      window.localStorage.removeItem('theme');
    }
    if (!cookieTheme && hasStoredTheme) {
      var isSuperDurableDomain = location.hostname === 'superdurable.io' || location.hostname.endsWith('.superdurable.io');
      document.cookie = 'superdurable-theme=' + storedTheme + '; Path=/; Max-Age=31536000; SameSite=Lax' +
        (isSuperDurableDomain ? '; Domain=.superdurable.io; Secure' : '');
    }
  } catch (_) {}
})();
(function () {
  try {
    var stored = window.localStorage.getItem('docs-locale');
    var cookieMatch = document.cookie.match(/(?:^|; )superdurable-docs-locale=(en|zh-Hans)(?:;|$)/);
    var preferred = stored === 'en' || stored === 'zh-Hans' ? stored : (cookieMatch ? cookieMatch[1] : null);
    if (preferred !== 'zh-Hans') return;
    var path = location.pathname;
    if (path === '/zh-Hans' || path === '/zh-Hans/' || path.indexOf('/zh-Hans/') === 0) return;
    var next = path === '/' ? '/zh-Hans/' : '/zh-Hans' + path;
    location.replace(next + location.search + location.hash);
  } catch (_) {}
})();`,
    },
  ],

  i18n: {
    defaultLocale: 'en',
    locales: ['en', 'zh-Hans'],
    localeConfigs: {
      en: {label: 'English', htmlLang: 'en'},
      'zh-Hans': {label: '简体中文', htmlLang: 'zh-Hans'},
    },
  },

  presets: [
    [
      'classic',
      {
        docs: {
          path: 'content',
          routeBasePath: '/',
          sidebarPath: './sidebars.ts',
          editUrl: 'https://github.com/superdurable/dex/tree/main/docs/',
        },
        blog: false,
        theme: {
          customCss: ['./src/css/custom.css', './src/css/site-header.css'],
        },
        sitemap: {
          changefreq: 'daily',
          priority: 0.5,
        },
      } satisfies Preset.Options,
    ],
  ],

  plugins: [
    (context: LoadContext) =>
      clientRedirects(context, {
        id: 'default',
        fromExtensions: [],
        toExtensions: [],
        redirects: redirects
          .filter(
            ({from, to}) =>
              redirectPathLocale(context, from) === context.i18n.currentLocale &&
              redirectPathLocale(context, to) === context.i18n.currentLocale,
          )
          .map(({from, to}) => ({
            from: pathWithinCurrentLocale(context, from),
            to: pathWithinCurrentLocale(context, to),
          })),
      }),
    () => ({
      name: 'local-flow-definition-renderer',
      configureWebpack: (_config, isServer, {getJSLoader}) => ({
        resolve: {
          symlinks: false,
          // Dex Web's Flow canvas is compiled from its source and shares the docs' React and libraries.
          alias: {
            '@dex-web-v2': fileURLToPath(new URL('../web/app/v2', import.meta.url)),
            ...Object.fromEntries(
              ['react', 'react-dom', '@xyflow/react', 'elkjs'].map((name) => [
                name,
                fileURLToPath(new URL(`./node_modules/${name}`, import.meta.url)),
              ]),
            ),
          },
        },
        module: {
          rules: [
            {
              ...getJSLoader({isServer}),
              test: /\.[jt]sx?$/,
              include: [
                fileURLToPath(new URL('./node_modules/@superdurable/flow-definition-renderer/src', import.meta.url)),
                fileURLToPath(new URL('../web/app/v2', import.meta.url)),
              ],
            },
          ],
        },
      }),
    }),
  ],

  themeConfig: {
    // A raster, deliberately: social cards will not render an SVG.
    image: 'img/brand/dex-social.png',
    colorMode: {
      defaultMode: 'light',
      // No toggle renders. disableSwitch would delete the shared theme choice and reset other tabs.
      disableSwitch: false,
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'Super Durable',
      logo: {
        alt: 'Super Durable',
        src: 'img/brand/dex-mark.svg',
        href: 'https://superdurable.io/',
        target: '_self',
      },
      items: [...companyHeaderNavigation.sites, ...companyHeaderNavigation.actions],
    },
    footer: {
      style: 'light',
      links: companyHeaderNavigation.footer,
      copyright: `© ${new Date().getFullYear()} Super Durable, Inc.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['java', 'python', 'go', 'typescript', 'rust', 'bash'],
    },
  } satisfies Preset.ThemeConfig,
};

function redirectPathLocale(context: LoadContext, pathname: string): string {
  return (
    context.i18n.locales.find(
      (locale) =>
        locale !== context.i18n.defaultLocale && pathname.startsWith(`/${locale}/`),
    ) ?? context.i18n.defaultLocale
  );
}

function pathWithinCurrentLocale(context: LoadContext, pathname: string): string {
  if (context.i18n.currentLocale === context.i18n.defaultLocale) {
    return pathname;
  }
  return pathname.slice(context.i18n.currentLocale.length + 1) || '/';
}

export default config;
