/**
 * Copyright (c) Facebook, Inc. and its affiliates.
 *
 * This source code is licensed under the MIT license found in the
 * LICENSE file in the root directory of this source tree.
 */

import React, {
  type ComponentProps,
  type ReactNode,
  useEffect,
  useMemo,
  useState,
} from 'react';
import clsx from 'clsx';
import {
  ThemeClassNames,
  Collapsible,
  useCollapsible,
} from '@docusaurus/theme-common';
import {isSamePath} from '@docusaurus/theme-common/internal';
import {
  findFirstSidebarItemLink,
  isActiveSidebarItem,
} from '@docusaurus/plugin-content-docs/client';
import Link from '@docusaurus/Link';
import {translate} from '@docusaurus/Translate';
import useIsBrowser from '@docusaurus/useIsBrowser';
import DocSidebarItems from '@theme/DocSidebarItems';
import type {Props} from '@theme/DocSidebarItem/Category';
import {
  hasSidebarHydrated,
  persistSidebarCategoryState,
  readSavedCategoryCollapsedBeforePaint,
  readSidebarCategoryState,
  rememberVisibleCategoryCollapsed,
} from '@site/src/components/sidebarState';
import {docsSectionIcons, isDocsSectionKey} from '@site/src/components/docsSections';

function SidebarSectionIcon({section}: {section: unknown}): ReactNode {
  if (!isDocsSectionKey(section)) {
    return null;
  }
  const Icon = docsSectionIcons[section];
  return (
    <span className="sidebar-section-icon" data-docs-section={section} aria-hidden="true">
      <Icon size={15} strokeWidth={2.25} />
    </span>
  );
}

function CollapseButton({
  collapsed,
  categoryLabel,
  onClick,
}: {
  collapsed: boolean;
  categoryLabel: string;
  onClick: ComponentProps<'button'>['onClick'];
}) {
  return (
    <button
      aria-label={
        collapsed
          ? translate(
              {
                id: 'theme.DocSidebarItem.expandCategoryAriaLabel',
                message: "Expand sidebar category '{label}'",
                description: 'The ARIA label to expand the sidebar category',
              },
              {label: categoryLabel},
            )
          : translate(
              {
                id: 'theme.DocSidebarItem.collapseCategoryAriaLabel',
                message: "Collapse sidebar category '{label}'",
                description: 'The ARIA label to collapse the sidebar category',
              },
              {label: categoryLabel},
            )
      }
      aria-expanded={!collapsed}
      type="button"
      className="clean-btn menu__caret"
      onClick={onClick}
    />
  );
}

function useCategoryHrefWithSSRFallback(
  item: Props['item'],
): string | undefined {
  const isBrowser = useIsBrowser();
  return useMemo(() => {
    if (item.href && !item.linkUnlisted) {
      return item.href;
    }
    if (isBrowser || !item.collapsible) {
      return undefined;
    }
    return findFirstSidebarItemLink(item);
  }, [item, isBrowser]);
}

export default function DocSidebarItemCategory({
  item,
  onItemClick,
  activePath,
  level,
  ...props
}: Props): ReactNode {
  const {items, label, collapsible, className, href} = item;
  const hrefWithSSRFallback = useCategoryHrefWithSSRFallback(item);
  const categoryKey = `category:${label}`;
  const isActive = isActiveSidebarItem(item, activePath);
  const isCurrentPage = isSamePath(href, activePath);
  const {collapsed, setCollapsed} = useCollapsible({
    initialState: () => {
      if (!collapsible || isActive) {
        return false;
      }
      return readSavedCategoryCollapsedBeforePaint(categoryKey) ?? item.collapsed ?? true;
    },
  });

  // Only the first hydrated page applies saved state late. Later mounts read it while rendering.
  const [isMountedAfterHydration] = useState(hasSidebarHydrated);
  useEffect(() => {
    if (isMountedAfterHydration) {
      return;
    }
    const savedCollapsedState = readSidebarCategoryState()[categoryKey];
    if (typeof savedCollapsedState === 'boolean' && !isActive) {
      setCollapsed(savedCollapsedState);
    }
    // Runs once per mount: a later page change must not reapply the saved state.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    rememberVisibleCategoryCollapsed(categoryKey, collapsed);
  }, [categoryKey, collapsed]);

  // The category of the current page stays open, even when the reader collapsed it before.
  useEffect(() => {
    if (isActive) {
      setCollapsed(false);
    }
  }, [isActive, activePath, setCollapsed]);

  const updateCollapsed = (toCollapsed: boolean = !collapsed) => {
    persistSidebarCategoryState(categoryKey, toCollapsed);
    setCollapsed(toCollapsed);
  };

  return (
    <li
      className={clsx(
        ThemeClassNames.docs.docSidebarItemCategory,
        ThemeClassNames.docs.docSidebarItemCategoryLevel(level),
        'menu__list-item',
        {
          'menu__list-item--collapsed': collapsed,
        },
        className,
      )}>
      <div
        className={clsx('menu__list-item-collapsible', {
          'menu__list-item-collapsible--active': isCurrentPage,
        })}>
        <Link
          className={clsx('menu__link', {
            'menu__link--sublist': collapsible,
            'menu__link--sublist-caret': !href && collapsible,
            'menu__link--active': isCurrentPage,
          })}
          onClick={
            collapsible
              ? (event) => {
                  onItemClick?.(item);
                  if (href) {
                    if (isCurrentPage) {
                      event.preventDefault();
                      updateCollapsed();
                    }
                  } else {
                    event.preventDefault();
                    updateCollapsed();
                  }
                }
              : () => {
                  onItemClick?.(item);
                }
          }
          aria-current={isCurrentPage ? 'page' : undefined}
          role={collapsible && !href ? 'button' : undefined}
          aria-expanded={collapsible && !href ? !collapsed : undefined}
          href={collapsible ? hrefWithSSRFallback ?? '#' : hrefWithSSRFallback}
          {...props}>
          <SidebarSectionIcon section={item.customProps?.section} />
          {label}
        </Link>
        {href && collapsible && (
          <CollapseButton
            collapsed={collapsed}
            categoryLabel={label}
            onClick={(event) => {
              event.preventDefault();
              updateCollapsed();
            }}
          />
        )}
      </div>

      <Collapsible lazy as="ul" className="menu__list" collapsed={collapsed}>
        <DocSidebarItems
          items={items}
          tabIndex={collapsed ? -1 : 0}
          onItemClick={onItemClick}
          activePath={activePath}
          level={level + 1}
        />
      </Collapsible>
    </li>
  );
}
