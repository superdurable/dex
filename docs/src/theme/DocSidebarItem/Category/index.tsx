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

const SIDEBAR_STATE_STORAGE_KEY = 'dex.docs.sidebar.category-state.v1';

type SidebarCategoryState = Record<string, boolean>;

function readSidebarCategoryState(): SidebarCategoryState {
  try {
    const serializedState = window.localStorage.getItem(SIDEBAR_STATE_STORAGE_KEY);
    if (serializedState === null) {
      return {};
    }
    const parsedState: unknown = JSON.parse(serializedState);
    if (
      parsedState === null ||
      typeof parsedState !== 'object' ||
      Array.isArray(parsedState)
    ) {
      return {};
    }
    return Object.fromEntries(
      Object.entries(parsedState).filter(
        ([, value]) => typeof value === 'boolean',
      ),
    );
  } catch {
    return {};
  }
}

function persistSidebarCategoryState(categoryKey: string, collapsed: boolean) {
  try {
    window.localStorage.setItem(
      SIDEBAR_STATE_STORAGE_KEY,
      JSON.stringify({...readSidebarCategoryState(), [categoryKey]: collapsed}),
    );
  } catch {
    // The sidebar remains usable when browser storage is unavailable.
  }
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
    initialState: () => (collapsible ? true : false),
  });

  useEffect(() => {
    const savedCollapsedState = readSidebarCategoryState()[categoryKey];
    if (typeof savedCollapsedState === 'boolean') {
      setCollapsed(savedCollapsedState);
    }
  }, [categoryKey, setCollapsed]);

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
            'menu__link--active': isActive,
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
