// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

class MarkdownExport {
  allContentLoaded({allContent}) {
    this.allContent = allContent;
  }

  async postBuild(properties) {
    const {exportMarkdown} = await import('./export.mjs');
    await exportMarkdown({...properties, allContent: this.allContent});
  }
}

module.exports = function markdownExport() {
  const exporter = new MarkdownExport();
  return {
    name: 'markdown-export',
    allContentLoaded: exporter.allContentLoaded.bind(exporter),
    postBuild: exporter.postBuild.bind(exporter),
  };
};
