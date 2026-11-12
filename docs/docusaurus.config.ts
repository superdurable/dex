/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

import {existsSync, readFileSync} from 'node:fs';
import referenceVersions from './reference/versions.json';
import {fileURLToPath} from 'node:url';
import clientRedirects from '@docusaurus/plugin-client-redirects';
import prismIdeTheme from './src/prismIdeTheme';
import type {Config, LoadContext} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';
import redirects from './redirects.json';

const developerReferenceComponents = Object.keys(referenceVersions) as (keyof typeof referenceVersions)[];

// Search contexts, in the order the search page lists them. The labels match the reference landing page.
const referenceSearchContexts = [
  {path: 'reference/go', label: {en: 'Go SDK', 'zh-Hans': 'Go SDK'}},
  {path: 'reference/python', label: {en: 'Python SDK', 'zh-Hans': 'Python SDK'}},
  {path: 'reference/java', label: {en: 'Java SDK', 'zh-Hans': 'Java SDK'}},
  {path: 'reference/typescript', label: {en: 'TypeScript SDK', 'zh-Hans': 'TypeScript SDK'}},
  {path: 'reference/rust', label: {en: 'Rust SDK', 'zh-Hans': 'Rust SDK'}},
  {path: 'reference/server', label: {en: 'Server', 'zh-Hans': 'Server'}},
  {path: 'reference/cli', label: {en: 'CLI', 'zh-Hans': 'CLI'}},
  {path: 'reference/web', label: {en: 'Dex Web', 'zh-Hans': 'Dex Web'}},
  {path: 'reference/agents', label: {en: 'Agents packages', 'zh-Hans': 'Agents 包'}},
];

// src/theme/Navbar/Content renders the header itself; only the locale switch comes from themeConfig.
const companyHeaderNavigation = {
  actions: [{type: 'localeDropdown', position: 'right'}],
  footer: [
    {
      title: 'Products',
      items: [
        {label: 'Dex Cloud', href: 'https://superdurable.io/dex-cloud/', target: '_self'},
        {label: 'Dex Connectors', href: 'https://connectors.superdurable.io/', target: '_self'},
      ],
    },
    {
      title: 'Developers',
      items: [
        {label: 'Dex Docs', to: '/'},
        {label: 'Quick start', to: '/quick-start'},
        {label: 'GitHub', href: 'https://github.com/superdurable/dex', target: '_self'},
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
} satisfies {actions: NonNullable<Preset.ThemeConfig['navbar']>['items']; footer: NonNullable<Preset.ThemeConfig['footer']>['links']};

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
          customCss: ['./src/css/custom.css', './src/css/site-header.css', './src/css/refresh.css', './src/css/ide-code.css'],
        },
        sitemap: {
          changefreq: 'daily',
          priority: 0.5,
          ignorePatterns: ['/api/**'],
        },
      } satisfies Preset.Options,
    ],
  ],

  clientModules: ['./src/clientModules/sdkFromQuery.ts'],

  themes: [
    [
      '@easyops-cn/docusaurus-search-local',
      {
        hashed: true,
        language: ['en', 'zh'],
        indexDocs: true,
        indexBlog: false,
        indexPages: false,
        docsRouteBasePath: '/',
        // Older reference snapshots are excluded; the latest reference and every guide are indexed.
        ignoreFiles: [/^reference\/[^/]+\/\d+\.\d+\.\d+\//],
        searchBarShortcutHint: true,
        searchBarPosition: 'left',
        highlightSearchTermsOnTargetPage: false,
        explicitSearchResultPath: true,
        // Each Developer reference component gets its own index, so reference pages never crowd guide results.
        // Guide pages search the guides; reference pages search their component; the search page switches.
        searchContextByPaths: referenceSearchContexts,
        useAllContextsWithNoSearchContext: false,
      },
    ],
  ],

  plugins: [
    // Each Developer reference component is its own docs instance, so it can be versioned on its own.
    ...developerReferenceComponents.map((component) => [
      '@docusaurus/plugin-content-docs',
      {
        id: `reference-${component}`,
        path: `reference/${component}`,
        routeBasePath: `reference/${component}`,
        sidebarPath: `./reference/sidebars/${component}.ts`,
        editUrl: 'https://github.com/superdurable/dex/tree/main/docs/',
        lastVersion: 'current',
        versions: {
          current: {label: `${referenceVersions[component]} (latest)`, path: '', banner: 'none', badge: false},
          ...Object.fromEntries(referenceSnapshots(component).map((version) => [
            version,
            {label: version, path: version, banner: 'unmaintained', badge: false, noIndex: true},
          ])),
        },
        onlyIncludeVersions: ['current', ...referenceSnapshots(component).filter(
          (version) => version !== referenceVersions[component],
        )],
      },
    ]),
    './plugins/shared-docs-sidebar/index.cjs',
    './plugins/markdown-export/index.cjs',
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
            '@dex-web-design': fileURLToPath(new URL('../web/app/design', import.meta.url)),
            '@dex-web-workspace': fileURLToPath(new URL('../web/app/workspace', import.meta.url)),
            ...Object.fromEntries(
              ['react', 'react-dom', '@xyflow/react', 'elkjs', 'lucide-react'].map((name) => [
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
                fileURLToPath(new URL('../web/app/design', import.meta.url)),
                fileURLToPath(new URL('../web/app/workspace', import.meta.url)),
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
      // The footer brand reads this title; the header wordmark has its own text.
      title: 'Super Durable',
      logo: {
        alt: 'Super Durable',
        src: 'img/brand/dex-mark.svg',
        href: 'https://superdurable.io/',
        target: '_self',
      },
      items: companyHeaderNavigation.actions,
    },
    footer: {
      style: 'light',
      links: companyHeaderNavigation.footer,
      copyright: `© ${new Date().getFullYear()} Super Durable, Inc.`,
    },
    prism: {
      theme: prismIdeTheme,
      darkTheme: prismIdeTheme,
      additionalLanguages: ['java', 'python', 'go', 'typescript', 'rust', 'bash'],
    },
  } satisfies Preset.ThemeConfig,
};

function referenceSnapshots(component: string): string[] {
  const versionsPath = new URL(`./reference-${component}_versions.json`, import.meta.url);
  return existsSync(versionsPath) ? JSON.parse(readFileSync(versionsPath, 'utf8')) : [];
}

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
