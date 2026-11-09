// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

// Publishes one docs sidebar for every docs instance. The main docs sidebar stays the frame,
// and each Developer Reference entry expands into that reference's own sidebar, per version.
const path = require('node:path');

// The package exports map hides lib/props, which converts loaded sidebars into the client shape.
const {toSidebarsProp} = require(
  path.join(path.dirname(require.resolve('@docusaurus/plugin-content-docs')), 'props.js'),
);

const docsPluginName = 'docusaurus-plugin-content-docs';
const referencePluginPrefix = 'reference-';

function isReferenceIndexLink(item) {
  return item.type === 'link' && /\/reference\/?$/.test(item.href);
}

function referenceSidebarsByVersion(content) {
  return Object.fromEntries(content.loadedVersions.map((version) => [
    version.versionName,
    toSidebarsProp(version).reference.filter((item) => !isReferenceIndexLink(item)),
  ]));
}

module.exports = function sharedDocsSidebar() {
  return {
    name: 'shared-docs-sidebar',
    allContentLoaded({allContent, actions}) {
      const docsContent = allContent[docsPluginName];
      const mainVersion = docsContent.default.loadedVersions.find(
        (version) => version.versionName === 'current',
      );
      const references = {};
      for (const [pluginId, content] of Object.entries(docsContent)) {
        if (pluginId.startsWith(referencePluginPrefix)) {
          references[pluginId.slice(referencePluginPrefix.length)] = referenceSidebarsByVersion(content);
        }
      }
      actions.setGlobalData({mainSidebar: toSidebarsProp(mainVersion).docs, references});
    },
  };
};
