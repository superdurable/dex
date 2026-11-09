import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  reference: [
    {type: 'link', label: '← Developer Reference', href: '/reference/'},
    'index',
    'dex-web',
    'flow-visualization',
    'local-connector-setup',
    'debugging',
    'changelog',
  ],
};

export default sidebars;
