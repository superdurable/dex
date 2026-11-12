/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type MouseEvent, type ReactNode, useEffect, useRef, useState} from 'react';
import clsx from 'clsx';
import {translate} from '@docusaurus/Translate';
import {useThemeConfig} from '@docusaurus/theme-common';
import useBrokenLinks from '@docusaurus/useBrokenLinks';
import type {Props} from '@theme/Heading';
import {Link2} from 'lucide-react';

import styles from './styles.module.css';

/**
 * A section heading with a section link. On hover, a muted link icon appears to the left of the heading.
 * Clicking it moves to the section, copies the section's address, and shows "Copied" for a moment.
 */
export default function Heading({as: As, id, ...props}: Props): ReactNode {
  const brokenLinks = useBrokenLinks();
  const {
    navbar: {hideOnScroll},
  } = useThemeConfig();
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);

  // H1 headings do not need an id because they don't appear in the TOC.
  if (As === 'h1' || !id) {
    return <As {...props} id={undefined} />;
  }

  brokenLinks.collectAnchor(id);

  const anchorTitle = translate(
    {
      id: 'theme.common.headingLinkTitle',
      message: 'Direct link to {heading}',
      description: 'Title for link to heading',
    },
    {
      heading: typeof props.children === 'string' ? props.children : id,
    },
  );
  const copiedLabel = translate({
    id: 'shell.heading.linkCopied',
    message: 'Copied',
    description: 'Shown next to a heading after its section link is copied',
  });

  const copyLink = (event: MouseEvent<HTMLAnchorElement>) => {
    // The default action still moves to the section and updates the address bar.
    const url = new URL(event.currentTarget.href, window.location.href).href;
    try {
      void navigator.clipboard?.writeText(url).then(() => {
        setCopied(true);
        clearTimeout(timer.current);
        timer.current = setTimeout(() => setCopied(false), 1500);
      }).catch(() => undefined);
    } catch {
      // Copying is a convenience; the link still works without it.
    }
  };

  return (
    <As
      {...props}
      className={clsx(
        'anchor',
        styles.heading,
        hideOnScroll ? styles.anchorWithHideOnScrollNavbar : styles.anchorWithStickyNavbar,
        props.className,
      )}
      id={id}>
      <a className={clsx('hash-link', styles.link)} href={`#${id}`} aria-label={anchorTitle} title={anchorTitle} onClick={copyLink}>
        <Link2 aria-hidden="true" className={styles.icon} />
      </a>
      {props.children}
      <span className={clsx(styles.copied, copied && styles.copiedVisible)} role="status" aria-live="polite">
        {copied ? copiedLabel : ''}
      </span>
    </As>
  );
}
