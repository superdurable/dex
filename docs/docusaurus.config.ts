import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

const config: Config = {
  title: 'Dex Docs',
  tagline: 'Durable Execution for backend engineering',
  favicon: 'img/favicon.png',

  future: {
    v4: true,
  },

  url: 'https://docs.superdurable.io',
  baseUrl: '/',

  organizationName: 'superdurable',
  projectName: 'dex',

  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'throw',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
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
          customCss: './src/css/custom.css',
        },
        sitemap: {
          changefreq: 'weekly',
          priority: 0.5,
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    image: 'img/brand/super-durable-logo.png',
    colorMode: {
      defaultMode: 'light',
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'Dex Docs',
      logo: {
        alt: 'Super Durable',
        src: 'img/brand/super-durable-logo.png',
      },
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'docs',
          position: 'left',
          label: 'Docs',
        },
        {
          href: 'https://superdurable.io',
          label: 'Website',
          position: 'right',
        },
        {
          href: 'https://github.com/superdurable/dex',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Docs',
          items: [
            {label: 'Intro', to: '/intro/what-is-dex'},
            {label: 'Quick Start', to: '/quick-start'},
            {label: 'Glossary', to: '/glossary'},
          ],
        },
        {
          title: 'Community',
          items: [
            {label: 'GitHub', href: 'https://github.com/superdurable/dex'},
            {label: 'Website', href: 'https://superdurable.io'},
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} Super Durable, Inc.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['java', 'python', 'go', 'typescript', 'bash'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
