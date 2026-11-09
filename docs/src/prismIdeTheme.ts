/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import type {PrismTheme} from 'prism-react-renderer';

// The editor palette of the superdurable.io home page, used in both color modes.
const ideColors = {
  background: '#0d1117',
  bar: '#161b22',
  text: '#e6edf3',
  muted: '#9da7b3',
  gutter: '#7d8590',
  keyword: '#93a8ff',
  function: '#d2a8ff',
  string: '#f2cc8f',
  comment: '#8b949e',
  number: '#79c0ff',
  selection: '#388bfd66',
} as const;

const prismIdeTheme: PrismTheme = {
  plain: {color: ideColors.text, backgroundColor: ideColors.background},
  styles: [
    {types: ['comment', 'prolog', 'doctype', 'cdata'], style: {color: ideColors.comment, fontStyle: 'italic'}},
    {types: ['keyword', 'builtin', 'important', 'atrule', 'selector'], style: {color: ideColors.keyword}},
    {types: ['function', 'class-name', 'maybe-class-name', 'decorator', 'annotation'], style: {color: ideColors.function}},
    {types: ['string', 'char', 'attr-value', 'regex', 'template-string'], style: {color: ideColors.string}},
    {types: ['number', 'boolean', 'constant', 'symbol'], style: {color: ideColors.number}},
    {types: ['punctuation', 'operator'], style: {color: ideColors.muted}},
    {types: ['variable', 'parameter', 'property', 'attr-name', 'tag'], style: {color: ideColors.text}},
    {types: ['deleted'], style: {color: '#ffa198'}},
    {types: ['inserted'], style: {color: '#7ee787'}},
  ],
};

export default prismIdeTheme;
