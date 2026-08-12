import React, {type ReactNode} from 'react';
import {ErrorCauseBoundary, useThemeConfig} from '@docusaurus/theme-common';
import {splitNavbarItems, useNavbarMobileSidebar} from '@docusaurus/theme-common/internal';
import NavbarItem, {type Props as NavbarItemConfig} from '@theme/NavbarItem';
import NavbarColorModeToggle from '@theme/Navbar/ColorModeToggle';
import NavbarMobileSidebarToggle from '@theme/Navbar/MobileSidebar/Toggle';
import NavbarLogo from '@theme/Navbar/Logo';
import GitHubStarNavbarItem from '@site/src/components/GitHubStarNavbarItem';

function NavbarItems({items}: {items: NavbarItemConfig[]}): ReactNode {
  return items.map((item, index) => (
    <ErrorCauseBoundary
      key={index}
      onError={(error) => new Error(`Unable to render navbar item: ${JSON.stringify(item)}`, {cause: error})}>
      <NavbarItem {...item} />
    </ErrorCauseBoundary>
  ));
}

export default function NavbarContent(): ReactNode {
  const mobileSidebar = useNavbarMobileSidebar();
  const items = useThemeConfig().navbar.items as NavbarItemConfig[];
  const [leftItems, rightItems] = splitNavbarItems(items);
  const bookingItems = rightItems.filter((item) => item.className === 'header-booking-link');
  const utilityItems = rightItems.filter((item) => item.className !== 'header-booking-link');

  return (
    <div className="navbar__inner brand-navbar-inner">
      <div className="brand-navbar-start">
        {!mobileSidebar.disabled && <NavbarMobileSidebarToggle />}
        <NavbarLogo />
      </div>
      <nav className="brand-navbar-center" aria-label="Primary navigation">
        <NavbarItems items={leftItems} />
      </nav>
      <div className="brand-navbar-actions">
        <NavbarItems items={utilityItems} />
        <GitHubStarNavbarItem />
        <NavbarColorModeToggle />
        <NavbarItems items={bookingItems} />
      </div>
    </div>
  );
}
