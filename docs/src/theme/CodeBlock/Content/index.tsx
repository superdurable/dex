/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode, useMemo, useState} from 'react';
import clsx from 'clsx';
import {useCodeBlockContext} from '@docusaurus/theme-common/internal';
import {usePrismTheme} from '@docusaurus/theme-common';
import {translate} from '@docusaurus/Translate';
import {Highlight} from 'prism-react-renderer';
import OriginalContent from '@theme-original/CodeBlock/Content';
import Line from '@theme/CodeBlock/Line';
import type {Props} from '@theme/CodeBlock/Content';
import {
  type IdeFoldRange,
  explicitFoldRanges,
  importFoldRange,
  longBlockFoldRange,
  mergeFoldRanges,
  useIdeCodeBlockInfo,
} from '@site/src/components/ideCode';

function useFoldRanges(code: string, language: string, metastring: string | undefined): IdeFoldRange[] {
  return useMemo(() => {
    const lines = code.split('\n');
    const explicit = explicitFoldRanges(metastring, lines.length);
    const imports = importFoldRange(lines, language);
    const ranges = mergeFoldRanges([...(imports ? [imports] : []), ...explicit]);
    if (explicit.length === 0) {
      const tail = longBlockFoldRange(lines.length);
      if (tail) {
        return mergeFoldRanges([...ranges, tail]);
      }
    }
    return ranges;
  }, [code, language, metastring]);
}

function showLinesLabel(range: IdeFoldRange): string {
  return translate(
    {id: 'ideCode.fold.showAria', message: 'Show lines {start} to {end}',
      description: 'Accessible name of the button that expands collapsed code lines'},
    {start: range.start, end: range.end},
  );
}

function hideLinesLabel(range: IdeFoldRange): string {
  return translate(
    {id: 'ideCode.fold.hideAria', message: 'Collapse lines {start} to {end}',
      description: 'Accessible name of the button that collapses expanded code lines'},
    {start: range.start, end: range.end},
  );
}

function visibleFoldText(range: IdeFoldRange): string {
  return translate(
    {id: 'ideCode.fold.show', message: '… {lines}', description: 'Text of the button that expands collapsed code lines'},
    {lines: range.label},
  );
}

function lineText(tokens: {content: string}[]): string {
  return tokens.map((token) => token.content).join('');
}

/** Which terminal lines get a prompt. Console transcripts carry their own prompts. */
function terminalPrompts(lines: string[], language: string): boolean[] {
  if (language === 'console') {
    return lines.map(() => false);
  }
  let continuesCommand = false;
  return lines.map((text) => {
    const trimmed = text.trim();
    if (trimmed === '' || trimmed.startsWith('#')) {
      return false;
    }
    const isCommand = !continuesCommand;
    continuesCommand = /\\\s*$/.test(text);
    return isCommand;
  });
}

/** Code with a line-number or prompt gutter, collapsible line runs, and an editable caret. */
export default function IdeCodeBlockContent(props: Props): ReactNode {
  const {metadata, wordWrap} = useCodeBlockContext();
  const {mode, metastring} = useIdeCodeBlockInfo();
  const prismTheme = usePrismTheme();
  const {code, language, lineClassNames} = metadata;
  const foldRanges = useFoldRanges(code, language, mode === 'editor' ? metastring : undefined);
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(new Set());
  // Remount the editable code after a fold change so reader edits cannot desync React.
  const [renderGeneration, setRenderGeneration] = useState(0);

  if (mode === 'plain') {
    return <OriginalContent {...props} />;
  }
  const activeFolds = mode === 'editor' ? foldRanges : [];
  const toggleFold = (start: number) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(start)) {
        next.delete(start);
      } else {
        next.add(start);
      }
      return next;
    });
    setRenderGeneration((generation) => generation + 1);
  };

  return (
    <Highlight theme={prismTheme} code={code} language={language}>
      {({className, style, tokens: lines, getLineProps, getTokenProps}) => {
        const prompts = mode === 'terminal' ? terminalPrompts(lines.map(lineText), language) : [];
        const gutter: ReactNode[] = [];
        const rows: ReactNode[] = [];
        const expandButtons: ReactNode[] = [];
        for (let index = 0; index < lines.length; index += 1) {
          const lineNumber = index + 1;
          const fold = activeFolds.find((range) => range.start === lineNumber);
          if (fold && !expanded.has(fold.start)) {
            const firstFoldedText = lineText(lines[index]);
            const indentWidth = firstFoldedText.length - firstFoldedText.trimStart().length;
            gutter.push(<span key={`g${lineNumber}`} className="ide-code__gutter-fold" aria-hidden="true">›</span>);
            // The visible row keeps the line rhythm; the real button sits outside the editable code.
            rows.push(<span key={`f${lineNumber}`} className="ide-code__fold-row" contentEditable={false}
              aria-hidden="true" />);
            expandButtons.push(
              <button key={`b${lineNumber}`} type="button" className="ide-code__fold-button"
                style={{top: `calc(${rows.length - 1} * 1lh)`, left: `${indentWidth}ch`}}
                onClick={() => toggleFold(fold.start)} aria-expanded="false" aria-label={showLinesLabel(fold)}>
                {visibleFoldText(fold)}
              </button>,
            );
            index = fold.end - 1;
            continue;
          }
          if (mode === 'terminal') {
            gutter.push(<span key={`g${lineNumber}`} aria-hidden="true">{prompts[index] ? '$' : ''}</span>);
          } else if (fold) {
            gutter.push(
              <button key={`g${lineNumber}`} type="button" className="ide-code__gutter-collapse"
                onClick={() => toggleFold(fold.start)} aria-expanded="true" aria-label={hideLinesLabel(fold)}>
                {lineNumber}
              </button>,
            );
          } else {
            gutter.push(<span key={`g${lineNumber}`} aria-hidden="true">{lineNumber}</span>);
          }
          rows.push(
            <Line key={`l${lineNumber}`} line={lines[index]} getLineProps={getLineProps}
              getTokenProps={getTokenProps} classNames={lineClassNames[index]} showLineNumbers={false} />,
          );
        }
        return (
          <div className={clsx('ide-code__body', props.className)} style={style}>
            <div className="ide-code__gutter">{gutter}</div>
            <pre ref={wordWrap.codeBlockRef} className={clsx(className, 'ide-code__scroll', 'thin-scrollbar')}
              tabIndex={-1}>
              <code key={renderGeneration} className="ide-code__code" contentEditable="plaintext-only"
                suppressContentEditableWarning spellCheck={false} autoCorrect="off" autoCapitalize="off"
                role="textbox" aria-multiline="true"
                aria-label={translate({id: 'ideCode.editor.label', message: 'Code, editable for trying changes',
                  description: 'Accessible name of an editable code block'})}>
                {rows}
              </code>
              {expandButtons}
            </pre>
          </div>
        );
      }}
    </Highlight>
  );
}
