// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import useBrokenLinks from '@docusaurus/useBrokenLinks';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

import compatibility from '@site/static/compatibility/server-zd.json';
import referenceVersions from '@site/reference/versions.json';
import {compatibilityMatrixTable, serverCompatibilityTable} from '@site/src/serverZdCompatibility.mjs';

// Wide tables scroll inside their own box instead of the page; a version range never wraps.
const scrollBoxStyle = {overflowX: 'auto'} as const;
const cellStyle = {whiteSpace: 'nowrap'} as const;

function useLocale(): string {
  return useDocusaurusContext().i18n.currentLocale;
}

/** Every server-zd release in the compatibility data, one row each, with a stable row anchor. */
export function ServerZdCompatibilityMatrix(): ReactNode {
  const brokenLinks = useBrokenLinks();
  const table = compatibilityMatrixTable(compatibility, useLocale());
  for (const row of table.rows) brokenLinks.collectAnchor(row.anchor);
  return (
    <div style={scrollBoxStyle}>
      <table>
        <thead>
          <tr>{table.header.map((label) => <th key={label}>{label}</th>)}</tr>
        </thead>
        <tbody>
          {table.rows.map((row) => (
            <tr key={row.anchor} id={row.anchor}>
              {row.cells.map((cell, index) => <td key={table.header[index]} style={cellStyle}>{cell}</td>)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** One server-zd release's compatible clients; without a version, the latest server reference version. */
export function ServerZdCompatibility({version}: {version?: string}): ReactNode {
  const table = serverCompatibilityTable(compatibility, version ?? referenceVersions.server, useLocale());
  return (
    <>
      <p>{table.summary}</p>
      {table.header ? (
        <div style={scrollBoxStyle}>
          <table>
            <thead>
              <tr>{table.header.map((label) => <th key={label}>{label}</th>)}</tr>
            </thead>
            <tbody>
              {table.rows.map((cells) => (
                <tr key={cells[0]}>{cells.map((cell, index) => <td key={table.header?.[index]}>{cell}</td>)}</tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </>
  );
}
